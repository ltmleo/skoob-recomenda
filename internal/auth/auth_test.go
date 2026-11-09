package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

func TestStoreRoundTripAndPermissions(t *testing.T) {
	t.Setenv(EnvToken, "")
	s := Store{Path: filepath.Join(t.TempDir(), "sub", "token")}
	if _, err := s.Load(); err == nil {
		t.Fatal("esperava erro sem login")
	}
	if err := s.Save("abc\n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); got != "abc" {
		t.Fatalf("got %q", got)
	}
	// Windows ignora bits Unix; lá a proteção vem da ACL do perfil do usuário.
	if fi, _ := os.Stat(s.Path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissão %v, esperava 0600", fi.Mode().Perm())
	}
	t.Setenv(EnvToken, "fromenv")
	if got, _ := s.Load(); got != "fromenv" {
		t.Fatalf("env deveria ter precedência, got %q", got)
	}
	if err := s.Delete(); err != nil || s.Delete() != nil {
		t.Fatal("delete deve ser idempotente")
	}
}

func TestFindCookieMatchesDomainSuffixOnly(t *testing.T) {
	cs := []*network.Cookie{
		{Name: "token", Value: "evil", Domain: "notskoob.com.br"},
		{Name: "token", Value: "good", Domain: ".www.skoob.com.br"},
	}
	if got := findCookie(cs, "token", "skoob.com.br"); got != "good" {
		t.Fatalf("got %q", got)
	}
}

func chromePath(t *testing.T) string {
	for _, p := range []string{os.Getenv("CHROME_PATH"), "/opt/pw-browsers/chromium"} {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	t.Skip("Chrome/Chromium não encontrado (defina CHROME_PATH)")
	return ""
}

// Simula o fluxo real: página de login que só define o cookie após o "submit".
func TestBrowserLoginCapturesHttpOnlyCookieAfterLogin(t *testing.T) {
	path := chromePath(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			// O usuário "entra" depois de um tempo; o servidor só então define o cookie.
			fmt.Fprint(w, `<p>login</p><script>setTimeout(()=>{location.href='/do'}, 300)</script>`)
		case "/do":
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "s3cr3t", Path: "/", HttpOnly: true})
			fmt.Fprint(w, "ok")
		}
	}))
	defer srv.Close()

	o := DefaultLoginOptions()
	o.URL, o.Domain = srv.URL+"/login", "127.0.0.1"
	o.Headless, o.NoSandbox, o.ChromePath = true, true, path
	o.Timeout, o.Poll = 20*time.Second, 100*time.Millisecond
	got, err := BrowserLogin(context.Background(), o)
	if err != nil || got != "s3cr3t" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestBrowserLoginTimesOut(t *testing.T) {
	path := chromePath(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "login") }))
	defer srv.Close()
	o := DefaultLoginOptions()
	o.URL, o.Domain = srv.URL, "127.0.0.1"
	o.Headless, o.NoSandbox, o.ChromePath = true, true, path
	o.Timeout, o.Poll = 3*time.Second, 100*time.Millisecond
	if _, err := BrowserLogin(context.Background(), o); err != ErrLoginTimeout {
		t.Fatalf("esperava ErrLoginTimeout, veio %v", err)
	}
}

func TestProfileSendsRawTokenAndMapsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "tok" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "forbidden")
			return
		}
		fmt.Fprint(w, `{"id":530077,"name":"Leo"}`)
	}))
	defer srv.Close()
	p, err := Profile(context.Background(), srv.URL, "tok")
	if err != nil || p["name"] != "Leo" {
		t.Fatalf("got %v, %v", p, err)
	}
	if _, err := Profile(context.Background(), srv.URL, "bad"); err == nil {
		t.Fatal("esperava erro de sessão inválida")
	}
}
