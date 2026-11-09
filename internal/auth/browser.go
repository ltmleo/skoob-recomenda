package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// LoginOptions configura o login interativo no navegador.
type LoginOptions struct {
	URL        string        // página de login
	CookieName string        // cookie que guarda a sessão
	Domain     string        // domínio do cookie (sufixo)
	Timeout    time.Duration // tempo máximo para o usuário concluir o login
	Poll       time.Duration // intervalo entre verificações do cookie
	Headless   bool          // só para testes: o usuário precisa ver a janela
	ChromePath string        // vazio = autodetecta Chrome/Chromium instalado
	NoSandbox  bool          // necessário em containers rodando como root
}

func DefaultLoginOptions() LoginOptions {
	return LoginOptions{
		URL:        "https://www.skoob.com.br/pt/login",
		CookieName: "token",
		Domain:     "skoob.com.br",
		Timeout:    5 * time.Minute,
		Poll:       time.Second,
	}
}

// ErrLoginTimeout indica que o usuário não concluiu o login a tempo.
var ErrLoginTimeout = errors.New("tempo esgotado esperando o login")

// BrowserLogin abre um Chrome com perfil temporário e isolado, navega até a
// página de login e espera o cookie de sessão aparecer. A senha é digitada
// pelo usuário direto no site: a CLI nunca a vê, só lê o cookie resultante
// via DevTools Protocol (funciona mesmo com cookie HttpOnly).
func BrowserLogin(ctx context.Context, o LoginOptions) (string, error) {
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:0:0], chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.Flag("headless", o.Headless),
		chromedp.Flag("hide-scrollbars", o.Headless),
		chromedp.Flag("mute-audio", o.Headless),
		chromedp.WindowSize(1000, 800),
	)
	if o.ChromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(o.ChromePath))
	}
	if o.NoSandbox {
		allocOpts = append(allocOpts, chromedp.NoSandbox)
	}

	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	actx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	bctx, cancelBrowser := chromedp.NewContext(actx)
	defer cancelBrowser()

	if err := chromedp.Do(bctx, chromedp.Navigate(o.URL)); err != nil {
		return "", fmt.Errorf("abrindo navegador (Chrome/Chromium instalado? use -chrome): %w", err)
	}

	tick := time.NewTicker(o.Poll)
	defer tick.Stop()
	for {
		res, err := chromedp.Call(bctx, network.GetCookies, network.GetCookiesParams{})
		if err != nil {
			if ctx.Err() != nil {
				return "", ErrLoginTimeout
			}
			return "", fmt.Errorf("navegador fechado antes do login: %w", err)
		}
		if t := UnwrapToken(findCookie(res.Cookies, o.CookieName, o.Domain)); t != "" {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return "", ErrLoginTimeout
		case <-tick.C:
		}
	}
}

func findCookie(cs []*network.Cookie, name, domain string) string {
	for _, c := range cs {
		d := strings.TrimPrefix(c.Domain, ".")
		if c.Name == name && c.Value != "" && (d == domain || strings.HasSuffix(d, "."+domain)) {
			return c.Value
		}
	}
	return ""
}
