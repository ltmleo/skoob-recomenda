// Comando recomenda: recomendações de livros por filtragem colaborativa.
//
//	recomenda login                      # abre o navegador para entrar no Skoob
//	recomenda whoami                     # valida a sessão salva
//	recomenda logout                     # apaga o token salvo
//	recomenda skoob                      # baixa estante + vizinhos e recomenda
//	recomenda serve                      # app: recomendações, estante, seguir um livro
//	recomenda eval -data skoob.csv       # mede a qualidade (esconde notas e confere)
//	recomenda [run] -data ratings.csv -user 530077 -n 20 -html out.html
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/auth"
	"github.com/ltmleo/skoob-recomenda/internal/crawl"
	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/eval"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
	"github.com/ltmleo/skoob-recomenda/internal/report"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

const usage = `uso:
  recomenda login  [-chrome PATH] [-timeout 5m] [-no-sandbox]
  recomenda whoami
  recomenda logout
  recomenda skoob  [-mine-only] [-out skoob.csv] [-html recomendacoes.html]   (recomenda skoob -h)
  recomenda serve  [-data skoob.csv] [-addr 127.0.0.1:8080] [-open=false]   (app no navegador)
  recomenda eval   [-data skoob.csv] [-user ID] [-temporal] [-grid]   (compara algoritmos)
  recomenda [run] -data ARQUIVO.csv -user ID [flags]   (recomenda run -h para flags)
  recomenda version
`

// version é gravada no release (-ldflags "-X main.version=v1.2.3").
var version = "dev"

// versionString devolve a versão do release ou, num `go install ...@v1.2.3`,
// a versão do módulo registrada pelo Go no binário.
func versionString() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	} else if len(args) > 0 && strings.TrimLeft(args[0], "-") == "version" {
		cmd = "version"
	}
	var err error
	switch cmd {
	case "login":
		err = login(ctx, args)
	case "whoami":
		err = whoami(ctx)
	case "logout":
		err = logout()
	case "skoob":
		err = skoob(ctx, args)
	case "serve":
		err = serve(ctx, args)
	case "eval":
		err = evaluate(args)
	case "run":
		err = run(args)
	case "version":
		fmt.Println("recomenda", versionString())
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func store() (auth.Store, error) { return auth.DefaultStore() }

func login(ctx context.Context, args []string) error {
	o := auth.DefaultLoginOptions()
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fs.StringVar(&o.ChromePath, "chrome", "", "caminho do Chrome/Chromium (vazio = autodetecta)")
	fs.DurationVar(&o.Timeout, "timeout", o.Timeout, "tempo máximo para concluir o login")
	fs.BoolVar(&o.NoSandbox, "no-sandbox", false, "desliga o sandbox do Chrome (containers como root)")
	_ = fs.Parse(args)

	s, err := store()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Abrindo o navegador em %s\nEntre na sua conta; a janela fecha sozinha ao concluir (limite %s).\n", o.URL, o.Timeout)
	tok, err := auth.BrowserLogin(ctx, o)
	if errors.Is(err, auth.ErrLoginTimeout) {
		return fmt.Errorf("%w; tente de novo com -timeout maior", err)
	}
	if err != nil {
		return err
	}
	if err := s.Save(tok); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Login concluído. Token salvo em %s (permissão 0600).\n", s.Path)
	return nil
}

func whoami(ctx context.Context) error {
	s, err := store()
	if err != nil {
		return err
	}
	tok, err := s.Load()
	if err != nil {
		return err
	}
	p, err := auth.Profile(ctx, auth.APIBase, tok)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func logout() error {
	s, err := store()
	if err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Token removido.")
	return nil
}

// addAlgoFlags registra as flags do algoritmo (comuns a run, skoob, serve e eval).
func addAlgoFlags(fs *flag.FlagSet, o *recommend.Options) {
	*o = recommend.DefaultOptions()
	fs.DurationVar(&o.HalfLife, "half-life", o.HalfLife, "meia-vida do peso das leituras (ex: 8760h = 1 ano; 0 desliga)")
	fs.IntVar(&o.K, "k", o.K, "número de vizinhos (user-based)")
	fs.IntVar(&o.MinOverlap, "min-overlap", o.MinOverlap, "mínimo de livros avaliados em comum com um vizinho")
	fs.IntVar(&o.MinSupport, "min-support", o.MinSupport, "mínimo de vizinhos (ou de livros seus similares) por candidato")
	fs.Float64Var(&o.Shrinkage, "shrinkage", o.Shrinkage, "penalização para poucos livros em comum (user-user)")
	fs.Float64Var(&o.Amplify, "amplify", o.Amplify, "expoente da similaridade user-user (1 = desliga; >1 realça os mais parecidos)")
	fs.Float64Var(&o.AbandonedStars, "abandoned-stars", o.AbandonedStars, "nota implícita de livro abandonado sem nota (0 = ignora)")
	fs.Float64Var(&o.ItemWeight, "item-weight", o.ItemWeight, "peso do item-based na mistura (0 = só user-based, 1 = só item-based)")
	fs.IntVar(&o.ItemK, "item-k", o.ItemK, "livros seus mais similares usados por candidato (item-based)")
	fs.Float64Var(&o.ItemShrinkage, "item-shrinkage", o.ItemShrinkage, "penalização para poucos leitores em comum (item-item)")
	fs.IntVar(&o.MaxPerAuthor, "max-per-author", o.MaxPerAuthor, "máximo de recomendações do mesmo autor (0 = sem limite)")
	fs.StringVar(&o.Ranking, "ranking", o.Ranking, "ordem do top-n: likes, deviation ou prediction")
	fs.Float64Var(&o.PopWeight, "pop-weight", o.PopWeight, "peso da popularidade no ranking (0 = só personalização)")
}

// recFlags são as flags de recomendação de `run` e `skoob`.
type recFlags struct {
	n       *int
	htmlOut *string
	opts    recommend.Options
}

func addRecFlags(fs *flag.FlagSet, htmlDefault string) *recFlags {
	rf := &recFlags{
		n:       fs.Int("n", 20, "quantidade de recomendações"),
		htmlOut: fs.String("html", htmlDefault, "grava relatório HTML neste caminho (vazio = não grava)"),
	}
	addAlgoFlags(fs, &rf.opts)
	return rf
}

// authorOf liga a diversidade por autor aos metadados carregados.
func authorOf(books map[string]domain.Book) func(string) string {
	return func(id string) string { return books[id].Author }
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	data := fs.String("data", "", "CSV com as leituras (user_id,book_id,title,author,rating,read_at)")
	user := fs.String("user", "", "ID do usuário-alvo")
	rf := addRecFlags(fs, "")
	_ = fs.Parse(args)

	if *data == "" || *user == "" {
		fs.Usage()
		os.Exit(2)
	}
	mem, err := source.LoadCSV(*data)
	if err != nil {
		return err
	}
	if len(mem.Ratings[*user]) == 0 {
		return fmt.Errorf("usuário %s não encontrado em %s", *user, *data)
	}
	return recommendAndReport(mem, *user, rf)
}

// skoob faz tudo de uma vez: descobre quem é você, baixa sua estante e a dos
// leitores parecidos (grafo BFS), salva o CSV e gera as recomendações.
func skoob(ctx context.Context, args []string) error {
	co := crawl.DefaultOptions()
	fs := flag.NewFlagSet("skoob", flag.ExitOnError)
	out := fs.String("out", "skoob.csv", "CSV onde salvar os dados coletados (reuse com run -data)")
	mineOnly := fs.Bool("mine-only", false, "só exporta a sua estante, sem montar o grafo")
	fs.IntVar(&co.SeedBooks, "seeds", co.SeedBooks, "livros seus (nota alta) usados como ponto de partida")
	fs.IntVar(&co.ReadersPerBook, "readers", co.ReadersPerBook, "leitores coletados por livro semente")
	fs.IntVar(&co.MaxUsers, "max-users", co.MaxUsers, "teto de estantes de outros leitores")
	fs.Float64Var(&co.MinSeedStars, "min-seed-stars", co.MinSeedStars, "nota mínima para um livro virar semente")
	interval := fs.Duration("interval", 500*time.Millisecond, "intervalo mínimo entre requisições ao Skoob")
	rf := addRecFlags(fs, "recomendacoes.html")
	_ = fs.Parse(args)

	s, err := store()
	if err != nil {
		return err
	}
	tok, err := s.Load()
	if err != nil {
		return err
	}
	me, err := auth.MyID(ctx, auth.APIBase, tok)
	if err != nil {
		return err
	}

	src := source.NewSkoob(auth.APIBase, tok)
	src.Interval = *interval
	src.Owner = me

	var mem *source.Memory
	if *mineOnly {
		fmt.Fprintln(os.Stderr, "Baixando sua estante...")
		rs, err := src.UserRatings(ctx, me)
		if err != nil {
			return err
		}
		mem = source.NewMemory()
		for _, r := range rs {
			mem.Add(r)
			if b, err := src.Book(ctx, r.BookID); err == nil {
				mem.MergeBook(b)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "Montando o grafo: sua estante -> %d livros semente -> até %d leitores (Ctrl+C salva o que já veio)\n",
			co.SeedBooks, co.MaxUsers)
		co.Progress = func(done, total int) {
			fmt.Fprintf(os.Stderr, "\r  estantes de leitores: %d/%d", done, total)
			if done == total {
				fmt.Fprintln(os.Stderr)
			}
		}
		mem, err = crawl.Collect(ctx, src, me, co)
		if err != nil && (mem == nil || len(mem.Ratings[me]) == 0) {
			return err
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "\naviso: coleta incompleta (%v); seguindo com o que já foi baixado\n", err)
		}
	}

	if err := saveCSV(*out, mem); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Dados: %s (%d leitores, %d livros seus, %d livros no total)\n",
		*out, len(mem.Ratings), len(mem.Ratings[me]), len(mem.Readers))
	if *mineOnly {
		return nil
	}
	if err := recommendAndReport(mem, me, rf); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nPróximo passo: recomenda serve -data %s   (app com estante e \"seguir um livro\")\n", *out)
	return nil
}

func saveCSV(path string, mem *source.Memory) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := mem.WriteCSV(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func recommendAndReport(mem *source.Memory, user string, rf *recFlags) error {
	o := rf.opts
	o.Now = time.Now()
	o.AuthorOf = authorOf(mem.Books)
	res := recommend.Recommend(mem.Ratings, user, *rf.n, o)
	d := report.Build(user, res, mem.Books)
	if err := report.Text(os.Stdout, d); err != nil {
		return err
	}
	if *rf.htmlOut != "" {
		f, err := os.Create(*rf.htmlOut)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := report.HTML(f, d); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\nHTML: %s\n", *rf.htmlOut)
	}
	return nil
}

// evaluate compara algoritmos escondendo parte das notas (ver internal/eval).
func evaluate(args []string) error {
	cfg := eval.DefaultConfig()
	var o recommend.Options
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	data := fs.String("data", "skoob.csv", "CSV com as leituras")
	fs.StringVar(&cfg.Target, "user", "", "seu ID: sempre entra no teste (opcional)")
	fs.IntVar(&cfg.Users, "users", cfg.Users, "quantos leitores sortear para o teste (-1 = todos)")
	fs.IntVar(&cfg.MinRatings, "min-ratings", cfg.MinRatings, "só testa leitores com pelo menos isso de notas")
	fs.Float64Var(&cfg.Holdout, "holdout", cfg.Holdout, "fração das notas escondida por leitor")
	fs.IntVar(&cfg.At, "at", cfg.At, "K das métricas Precision@K, Recall@K e nDCG@K")
	fs.BoolVar(&cfg.Temporal, "temporal", false, "esconde as leituras mais recentes (simula o futuro) em vez de sortear")
	fs.Int64Var(&cfg.Seed, "seed", cfg.Seed, "semente do sorteio")
	grid := fs.Bool("grid", false, "varre item-weight, k, amplify e shrinkage em torno das flags")
	addAlgoFlags(fs, &o)
	_ = fs.Parse(args)

	mem, err := source.LoadCSV(*data)
	if err != nil {
		return err
	}
	o.Now = time.Now()
	vs := []eval.Variant{eval.PopularVariant()}
	add := func(name string, f func(x *recommend.Options)) {
		x := o
		f(&x)
		vs = append(vs, eval.KNN(name, x))
	}
	if *grid {
		for _, rk := range []string{recommend.RankPrediction, recommend.RankDeviation, recommend.RankLikes} {
			for _, a := range []float64{0, 0.25, 0.5, 0.75, 1} {
				add(fmt.Sprintf("%s α=%.2g", rk, a), func(x *recommend.Options) { x.Ranking, x.ItemWeight = rk, a })
			}
		}
		for _, k := range []int{20, 50, 100, 200} {
			for _, amp := range []float64{1, 2} {
				add(fmt.Sprintf("k=%d ρ=%g", k, amp), func(x *recommend.Options) { x.K, x.Amplify = k, amp })
			}
		}
		for _, sh := range []float64{2, 5, 10, 25} {
			for _, ish := range []float64{2, 10, 25} {
				add(fmt.Sprintf("β=%g βᵢ=%g", sh, ish), func(x *recommend.Options) { x.Shrinkage, x.ItemShrinkage = sh, ish })
			}
		}
		for _, ik := range []int{10, 30, 60} {
			add(fmt.Sprintf("item-k=%d", ik), func(x *recommend.Options) { x.ItemK = ik })
		}
		add("sem abandono implícito", func(x *recommend.Options) { x.AbandonedStars = 0 })
		add("sem peso temporal", func(x *recommend.Options) { x.HalfLife = 0 })
	} else {
		add("user-knn", func(x *recommend.Options) { x.ItemWeight = 0 })
		add("item-knn", func(x *recommend.Options) { x.ItemWeight = 1 })
		add(fmt.Sprintf("híbrido (α=%.2g)", o.ItemWeight), func(*recommend.Options) {})
	}

	split := "aleatório"
	if cfg.Temporal {
		split = "temporal"
	}
	who := "todos os leitores"
	if cfg.Users >= 0 {
		who = fmt.Sprintf("até %d leitores", cfg.Users)
	}
	fmt.Fprintf(os.Stderr, "Avaliando %d variantes: %s, %.0f%% das notas escondidas (corte %s)...\n",
		len(vs), who, cfg.Holdout*100, split)
	ms := eval.Run(mem.Ratings, cfg, vs...)

	w := len([]rune("variante"))
	for _, m := range ms {
		w = max(w, len([]rune(m.Name)))
	}
	pad := func(s string) string { return s + strings.Repeat(" ", w-len([]rune(s))) }
	at := fmt.Sprint(cfg.At)
	fmt.Printf("%s  %7s  %7s  %7s  %6s  %6s  %7s  leitores\n", pad("variante"), "P@"+at, "R@"+at, "nDCG@"+at, "MAE", "RMSE", "cobert.")
	for _, m := range ms {
		fmt.Printf("%s  %7.4f  %7.4f  %7.4f  %6.3f  %6.3f  %6.1f%%  %d\n", pad(m.Name),
			m.Precision, m.Recall, m.NDCG, m.MAE, m.RMSE, m.Coverage*100, m.Users)
	}
	return nil
}
