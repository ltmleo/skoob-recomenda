package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
)

func sample() Data {
	res := recommend.Result{
		Neighbors: []recommend.Neighbor{{UserID: "v1", Sim: 0.8}},
		Recommendations: []recommend.Recommendation{{
			BookID: "fund", Predicted: 4.6, Support: 3,
			Because:   []recommend.Evidence{{UserID: "v1", Sim: 0.8, Stars: 5, Dev: 1}},
			BecauseOf: []recommend.BookEvidence{{BookID: "duna", Sim: 0.4, Stars: 5}, {BookID: "sem-meta", Stars: 4.5}},
		}},
	}
	books := map[string]domain.Book{
		"fund": {ID: "fund", Title: "Fundação", Author: "Asimov"},
		"duna": {ID: "duna", Title: "Duna <ed. 1>"},
	}
	return Build("me", res, books)
}

func TestBuildResolvesTitles(t *testing.T) {
	d := sample()
	if len(d.Rows) != 1 || d.Rows[0].Rank != 1 || d.Rows[0].Book.Title != "Fundação" {
		t.Fatalf("linhas: %+v", d.Rows)
	}
	of := d.Rows[0].BecauseOf
	if len(of) != 2 || of[0].Title != "Duna <ed. 1>" || of[1].Title != "sem-meta" {
		t.Fatalf("títulos das explicações (sem metadado cai no ID): %+v", of)
	}
}

func TestTextAndHTML(t *testing.T) {
	var txt bytes.Buffer
	if err := Text(&txt, sample()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Fundação — Asimov", "nota prevista 4.6", "você gostou de: Duna <ed. 1> (5.0)", "v1 (sim 0.80) deu 5.0"} {
		if !strings.Contains(txt.String(), want) {
			t.Errorf("texto sem %q:\n%s", want, txt.String())
		}
	}
	var html bytes.Buffer
	if err := HTML(&html, sample()); err != nil {
		t.Fatal(err)
	}
	if h := html.String(); !strings.Contains(h, "Porque você gostou de Duna &lt;ed. 1&gt;") || strings.Contains(h, "<ed. 1>") {
		t.Fatalf("HTML deveria explicar e escapar o título:\n%s", h)
	}
	var empty bytes.Buffer
	_ = Text(&empty, Data{UserID: "me"})
	if !strings.Contains(empty.String(), "Nenhuma recomendação") {
		t.Fatal("sem linhas deveria explicar o motivo")
	}
}
