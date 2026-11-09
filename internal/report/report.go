// Package report renderiza as recomendações em texto e HTML.
package report

import (
	"fmt"
	"html/template"
	"io"
	"strings"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
)

type Row struct {
	Rank      int
	Book      domain.Book
	Predicted float64
	Support   int
	Because   []recommend.Evidence
	BecauseOf []Liked // livros seus parecidos com o recomendado (item-based)
}

// Liked é um livro seu que explica uma recomendação.
type Liked struct {
	Title string
	Stars float64
}

type Data struct {
	UserID    string
	Neighbors int
	Rows      []Row
}

func Build(userID string, res recommend.Result, books map[string]domain.Book) Data {
	d := Data{UserID: userID, Neighbors: len(res.Neighbors)}
	for i, r := range res.Recommendations {
		b, ok := books[r.BookID]
		if !ok {
			b = domain.Book{ID: r.BookID, Title: r.BookID}
		}
		row := Row{Rank: i + 1, Book: b, Predicted: r.Predicted, Support: r.Support, Because: r.Because}
		for _, e := range r.BecauseOf {
			t := e.BookID
			if x, ok := books[e.BookID]; ok && x.Title != "" {
				t = x.Title
			}
			row.BecauseOf = append(row.BecauseOf, Liked{Title: t, Stars: e.Stars})
		}
		d.Rows = append(d.Rows, row)
	}
	return d
}

func Text(w io.Writer, d Data) error {
	fmt.Fprintf(w, "Recomendações para %s (%d vizinhos similares)\n\n", d.UserID, d.Neighbors)
	if len(d.Rows) == 0 {
		_, err := fmt.Fprintln(w, "Nenhuma recomendação: poucos vizinhos com livros em comum. Colete mais dados ou reduza -min-overlap.")
		return err
	}
	for _, r := range d.Rows {
		title := r.Book.Title
		if r.Book.Author != "" {
			title += " — " + r.Book.Author
		}
		fmt.Fprintf(w, "%2d. %-50s  nota prevista %.1f  (%d leitores)\n", r.Rank, title, r.Predicted, r.Support)
		if len(r.BecauseOf) > 0 {
			var bs []string
			for _, b := range r.BecauseOf {
				bs = append(bs, fmt.Sprintf("%s (%.1f)", b.Title, b.Stars))
			}
			fmt.Fprintf(w, "    você gostou de: %s\n", strings.Join(bs, "; "))
		}
		if len(r.Because) > 0 {
			var ev []string
			for _, e := range r.Because {
				ev = append(ev, fmt.Sprintf("%s (sim %.2f) deu %.1f", e.UserID, e.Sim, e.Stars))
			}
			fmt.Fprintf(w, "    leitores parecidos: %s\n", strings.Join(ev, "; "))
		}
	}
	return nil
}

var page = template.Must(template.New("p").Funcs(template.FuncMap{
	"pct": func(x float64) float64 { return x / 5 * 100 },
}).Parse(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Recomendações de Livros</title>
<style>
:root{--bg:#fafaf7;--fg:#1d1d1b;--muted:#6b6b66;--card:#fff;--line:#e6e4dc;--accent:#2f6f4f}
@media (prefers-color-scheme:dark){:root{--bg:#141413;--fg:#ecebe6;--muted:#9b9a93;--card:#1e1e1c;--line:#2e2e2b;--accent:#6fbf94}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,sans-serif}
main{max-width:860px;margin:0 auto;padding:32px 16px}h1{margin:0 0 4px;font-size:1.6rem}
.sub{color:var(--muted);margin:0 0 24px}ol{list-style:none;padding:0;margin:0;display:grid;gap:12px}
li{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px 16px;display:grid;grid-template-columns:2.2rem 1fr auto;gap:12px;align-items:start}
.rank{font-weight:700;color:var(--muted);font-variant-numeric:tabular-nums}.t{font-weight:600}.a{color:var(--muted);font-size:.92rem}
.why{color:var(--muted);font-size:.85rem;margin-top:6px}.score{text-align:right;font-variant-numeric:tabular-nums;min-width:5.5rem}
.bar{height:6px;background:var(--line);border-radius:3px;margin-top:6px;overflow:hidden}.bar i{display:block;height:100%;background:var(--accent)}
</style></head><body><main>
<h1>Recomendações de Livros</h1>
<p class="sub">Usuário {{.UserID}} · {{.Neighbors}} leitores com gosto parecido</p>
{{if not .Rows}}<p>Nenhuma recomendação: poucos vizinhos com livros em comum.</p>{{end}}
<ol>{{range .Rows}}<li>
<div class="rank">{{.Rank}}</div>
<div><div class="t">{{.Book.Title}}</div>{{if .Book.Author}}<div class="a">{{.Book.Author}}</div>{{end}}
{{if .BecauseOf}}<div class="why">Porque você gostou de {{range $i,$e := .BecauseOf}}{{if $i}}, {{end}}{{$e.Title}}{{end}}</div>{{end}}
{{if .Because}}<div class="why">Leitores parecidos: {{range $i,$e := .Because}}{{if $i}} · {{end}}{{$e.UserID}} (sim {{printf "%.2f" $e.Sim}}) deu {{printf "%.1f" $e.Stars}}{{end}}</div>{{end}}</div>
<div class="score"><strong>{{printf "%.1f" .Predicted}}</strong> / 5<div class="bar"><i style="width:{{printf "%.0f" (pct .Predicted)}}%"></i></div><div class="a">{{.Support}} leitores</div></div>
</li>{{end}}</ol></main></body></html>
`))

func HTML(w io.Writer, d Data) error { return page.Execute(w, d) }
