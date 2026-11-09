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
	oracle := Variant{Name: "oráculo", Rec: func(rs map[string][]domain.Rating, u string) []recommend.Recommendation {
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
	anti := Variant{Name: "anti", Rec: func(rs map[string][]domain.Rating, u string) []recommend.Recommendation {
		recs := oracle.Rec(rs, u)
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
