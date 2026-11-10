package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/auth"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
	"github.com/ltmleo/skoob-recomenda/internal/source"
	"github.com/ltmleo/skoob-recomenda/internal/web"
)

// serve abre o app local: recomendações, sua estante com status e a página
// de cada livro, que segue o grafo (e pode expandi-lo pelo Skoob).
func serve(ctx context.Context, args []string) error {
	var rec recommend.Options
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := fs.String("data", "skoob.csv", "CSV gerado por `recomenda skoob` (regravado ao expandir o grafo)")
	user := fs.String("user", "", "seu ID (vazio = descobre pelo login)")
	addr := fs.String("addr", "127.0.0.1:8080", "endereço de escuta (porta 0 = qualquer livre)")
	open := fs.Bool("open", true, "abre o navegador automaticamente")
	n := fs.Int("n", 40, "quantidade de recomendações")
	interval := fs.Duration("interval", 500*time.Millisecond, "intervalo mínimo entre requisições ao Skoob")
	addAlgoFlags(fs, &rec)
	_ = fs.Parse(args)

	mem, err := source.LoadCSV(*data)
	if err != nil {
		return fmt.Errorf("lendo %s: rode `recomenda skoob` antes (%w)", *data, err)
	}
	if _, err := loadStats(*data, &rec); err != nil {
		return err
	}

	// Token é opcional: sem ele o app funciona, só não expande o grafo.
	var src *source.Skoob
	me := *user
	if s, err := store(); err == nil {
		if tok, err := s.Load(); err == nil {
			if me == "" {
				if id, err := auth.MyID(ctx, auth.APIBase, tok); err == nil {
					me = id
				} else {
					fmt.Fprintf(os.Stderr, "aviso: %v\n", err)
				}
			}
			src = source.NewSkoob(auth.APIBase, tok)
			src.Interval = *interval
			src.Owner = me
			src.Seed(mem.Books)
		}
	}
	if me == "" {
		return errors.New("não sei quem é você: rode `recomenda login` ou passe -user SEU_ID")
	}
	// Sua estante muda com frequência (status, notas): atualiza ao abrir.
	if src != nil {
		if rs, err := src.UserRatings(ctx, me); err == nil && len(rs) > 0 {
			mem.ReplaceUser(me, rs)
			for _, r := range rs {
				if b, err := src.Book(ctx, r.BookID); err == nil {
					mem.MergeBook(b)
				}
			}
			fmt.Fprintf(os.Stderr, "Sua estante atualizada do Skoob (%d livros).\n", len(rs))
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "aviso: não atualizei sua estante (%v); usando %s\n", err, *data)
		}
	}
	if len(mem.Ratings[me]) == 0 {
		return fmt.Errorf("usuário %s não está em %s", me, *data)
	}
	if src == nil {
		fmt.Fprintln(os.Stderr, "aviso: sem login no Skoob; o botão \"Expandir grafo\" fica desativado")
	}

	cfg := web.Config{Me: me, DataPath: *data, Mem: mem, N: *n, Rec: rec, AnyHost: !web.IsLoopback(*addr)}
	if cfg.AnyHost {
		fmt.Fprintf(os.Stderr, "aviso: %s não é loopback; o app fica acessível na rede, sem senha\n", *addr)
	}
	if src != nil {
		cfg.Src = src
	}
	app := web.New(ctx, cfg)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("escutando em %s (porta ocupada? use -addr 127.0.0.1:0): %w", *addr, err)
	}
	url := "http://" + ln.Addr().String() + "/"
	srv := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	fmt.Fprintf(os.Stderr, "App em %s com %s (Ctrl+C para parar)\n", url, *data)
	if *open {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "não consegui abrir o navegador (%v); abra o link acima\n", err)
		}
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
