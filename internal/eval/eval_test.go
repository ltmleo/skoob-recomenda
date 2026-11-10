package eval

import (
	"fmt"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
)

// Dez leitores leem os mesmos 30 livros: os pares de que todos gostam e os
// ímpares de que ninguém gosta.
func data() map[string][]domain.Rating {
	out := map[string][]domain.Rating{}
	for u := range 10 {
		uid := fmt.Sprintf("u%d", u)
		for b := range 30 {
			s := 1.0
			if b%2 == 0 {
				s = 5
			}
			out[uid] = append(out[uid], domain.Rating{UserID: uid, BookID: fmt.Sprintf("b%02d", b), Stars: s})
		}
	}
	return out
}

func TestOracleBeatsAntiOracle(t *testing.T) {
	// O oráculo conhece a verdade: põe os pares no topo com a nota certa.
	oracle := Variant{Name: "oráculo", Rec: func(rs map[string][]domain.Rating, u string, _ []domain.Rating) []recommend.Recommendation {
		seen := map[string]bool{}
		for _, r := range rs[u] {
			seen[r.BookID] = true
		}
		var good, bad []recommend.Recommendation
		for b := range 30 {
			id := fmt.Sprintf("b%02d", b)
			if seen[id] {
				continue
			}
			if b%2 == 0 {
				good = append(good, recommend.Recommendation{BookID: id, Predicted: 5})
			} else {
				bad = append(bad, recommend.Recommendation{BookID: id, Predicted: 1})
			}
		}
		return append(good, bad...)
	}}
	anti := Variant{Name: "anti", Rec: func(rs map[string][]domain.Rating, u string, _ []domain.Rating) []recommend.Recommendation {
		recs := oracle.Rec(rs, u, nil)
		for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
			recs[i], recs[j] = recs[j], recs[i]
		}
		for i := range recs {
			recs[i].Predicted = 6 - recs[i].Predicted
		}
		return recs
	}}
	cfg := DefaultConfig()
	cfg.MinRatings, cfg.At = 10, 3
	ms := Run(data(), cfg, oracle, anti)
	o, a := ms[0], ms[1]
	if o.Users != 10 || o.Coverage != 1 || o.MAE != 0 || o.NDCG < 0.999 || o.Recall < 0.5 {
		t.Fatalf("oráculo: %+v", o)
	}
	if a.NDCG >= o.NDCG/2 || a.Precision >= o.Precision || a.MAE != 4 {
		t.Fatalf("anti-oráculo deveria ser bem pior: %+v vs %+v", a, o)
	}
}

func TestSplitsAreDeterministicAndHideFromTrainOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinRatings, cfg.Users, cfg.Target = 10, 3, "u9"
	a, b := makeSplits(data(), cfg), makeSplits(data(), cfg)
	if len(a) != 4 || a[0].user != "u9" {
		t.Fatalf("Target sempre entra primeiro, + 3 sorteados: %d %s", len(a), a[0].user)
	}
	for i := range a {
		if a[i].user != b[i].user || fmt.Sprint(a[i].hidden) != fmt.Sprint(b[i].hidden) {
			t.Fatal("mesma semente deveria dar o mesmo corte")
		}
		if len(a[i].hidden) != 6 || len(a[i].train[a[i].user]) != 24 {
			t.Fatalf("20%% de 30 = 6 escondidos: %d / %d", len(a[i].hidden), len(a[i].train[a[i].user]))
		}
		for u, rs := range a[i].train {
			if u != a[i].user && len(rs) != 30 {
				t.Fatal("só o leitor testado perde notas")
			}
		}
	}
	full := data()
	_ = makeSplits(full, cfg)
	if len(full["u9"]) != 30 {
		t.Fatal("makeSplits não pode alterar os dados originais")
	}
}

func TestTemporalHidesMostRecent(t *testing.T) {
	d := data()
	for i := range d["u0"] {
		d["u0"][i].ReadAt = d["u0"][i].ReadAt.AddDate(0, 0, i) // b29 é o mais recente
	}
	cfg := DefaultConfig()
	cfg.MinRatings, cfg.Users, cfg.Target, cfg.Temporal = 10, 0, "u0", true
	sp := makeSplits(d, cfg)
	if len(sp) != 1 || sp[0].hidden[len(sp[0].hidden)-1].BookID != "b29" || sp[0].hidden[0].BookID != "b24" {
		t.Fatalf("corte temporal deveria esconder b24..b29: %+v", sp[0].hidden)
	}
}

func TestTemporalDropsOthersFuture(t *testing.T) {
	d := data()
	for u, rs := range d {
		for i := range rs {
			rs[i].ReadAt = rs[i].ReadAt.AddDate(2000, 0, i) // b29 é o mais recente de todos
		}
		d[u] = rs
	}
	cfg := DefaultConfig()
	cfg.MinRatings, cfg.Users, cfg.Target, cfg.Temporal = 10, 0, "u0", true
	sp := makeSplits(d, cfg)[0]
	cut := sp.hidden[0].ReadAt
	for u, rs := range sp.train {
		for _, r := range rs {
			if u != "u0" && r.ReadAt.After(cut) {
				t.Fatalf("%s manteve %s, lido depois do corte", u, r.BookID)
			}
		}
	}
	if n := len(sp.train["u1"]); n != 25 {
		t.Fatalf("u1 deveria ficar com b00..b24 (25), ficou com %d", n)
	}
}

func TestWithoutUserRemovesHiddenRatingFromAverage(t *testing.T) {
	g := func(b string) (domain.BookStats, bool) {
		switch b {
		case "solo":
			return domain.BookStats{Avg: 5, Count: 1}, true
		case "x":
			return domain.BookStats{Avg: 4, Count: 4}, true // notas 5,5,5,1
		}
		return domain.BookStats{}, false
	}
	f := withoutUser(g, []domain.Rating{{BookID: "x", Stars: 1}, {BookID: "solo", Stars: 5}})
	if st, ok := f("x"); !ok || st.Count != 3 || st.Avg != 5 {
		t.Fatalf("sem a nota 1 do leitor, sobram três 5: %+v", st)
	}
	if _, ok := f("solo"); ok {
		t.Fatal("se a única nota era do leitor, não há nota geral")
	}
	if st, ok := f("y"); ok || st.Count != 0 {
		t.Fatal("livro sem dados continua sem dados")
	}
}

func TestCompareDetectsConsistentGain(t *testing.T) {
	ref := Metrics{PerUser: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.1, 0.2, 0.3}}
	better := Metrics{PerUser: []float64{0.2, 0.3, 0.4, 0.5, 0.6, 0.2, 0.3, 0.4}}
	if d := Compare(ref, better, 500, 1); !d.Significant() || d.Mean < 0.099 || d.Lo <= 0 {
		t.Fatalf("ganho de 0,1 em todos deveria ser significativo: %+v", d)
	}
	noisy := Metrics{PerUser: []float64{0.3, 0.0, 0.5, 0.2, 0.7, 0.0, 0.2, 0.5}}
	if d := Compare(ref, noisy, 500, 1); d.Significant() {
		t.Fatalf("ganho que some no ruído não deveria contar: %+v", d)
	}
}

func TestSeedsAddSplits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinRatings, cfg.Users, cfg.At, cfg.Seeds = 10, 3, 3, 4
	ms := Run(data(), cfg, PopularVariant())
	if ms[0].Users != 12 || len(ms[0].PerUser) != 12 {
		t.Fatalf("4 sementes × 3 leitores = 12: %d", ms[0].Users)
	}
}
