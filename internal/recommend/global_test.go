package recommend

import (
	"math"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// Candidatos com a mesma evidência local: "a" e "b" foram amados pelos
// mesmos vizinhos. A nota geral do Skoob desempata.
func globalData() map[string][]domain.Rating {
	return group(
		r("me", "x", 5, 1), r("me", "y", 4, 1), r("me", "z", 1, 1),
		r("v1", "x", 5, 0), r("v1", "y", 4, 0), r("v1", "z", 1, 0), r("v1", "a", 5, 0), r("v1", "b", 5, 0), r("v1", "c", 4, 0),
		r("v2", "x", 4.5, 0), r("v2", "y", 4, 0), r("v2", "z", 2, 0), r("v2", "a", 5, 0), r("v2", "b", 5, 0), r("v2", "c", 4, 0),
		r("v3", "x", 5, 0), r("v3", "y", 3.5, 0), r("v3", "z", 1, 0), r("v3", "a", 4.5, 0), r("v3", "b", 4.5, 0),
	)
}

func statsOf(m map[string]domain.BookStats) func(string) (domain.BookStats, bool) {
	return func(b string) (domain.BookStats, bool) { s, ok := m[b]; return s, ok }
}

func TestGlobalWeightBreaksTiesByOverallRating(t *testing.T) {
	o := opts()
	o.MinOverlap = 2
	o.GlobalWeight = 0.3
	o.Global = statsOf(map[string]domain.BookStats{
		"a": {Avg: 3.2, Count: 900},
		"b": {Avg: 4.7, Count: 900},
	})
	got := ids(Recommend(globalData(), "me", 0, o).Recommendations)
	if len(got) != 3 || got[0] != "b" {
		t.Fatalf("b tem nota geral melhor e deveria vir primeiro: %v", got)
	}
	o.Global = statsOf(map[string]domain.BookStats{
		"a": {Avg: 4.7, Count: 900},
		"b": {Avg: 3.2, Count: 900},
	})
	if got := ids(Recommend(globalData(), "me", 0, o).Recommendations); got[0] != "a" {
		t.Fatalf("invertendo as notas, a deveria vir primeiro: %v", got)
	}
}

// A média bayesiana não deixa poucas notas altas vencerem muitas notas boas.
func TestGlobalPriorShrinksSmallCounts(t *testing.T) {
	o := opts()
	o.MinOverlap = 2
	o.GlobalWeight, o.GlobalPrior = 0.5, 20
	o.Global = statsOf(map[string]domain.BookStats{
		"a": {Avg: 5, Count: 2},
		"b": {Avg: 4.5, Count: 2000},
		"c": {Avg: 3, Count: 500}, // puxa a média do livro típico para baixo
	})
	if got := ids(Recommend(globalData(), "me", 0, o).Recommendations); got[0] != "b" {
		t.Fatalf("2 notas 5 não deveriam vencer 2000 notas 4,5: %v", got)
	}
}

func TestGlobalBlendMovesPrediction(t *testing.T) {
	o := opts()
	o.MinOverlap = 2
	o.GlobalBlend = 1
	base := Recommend(globalData(), "me", 0, o).Recommendations // sem Global: igual a não usar
	o.Global = statsOf(map[string]domain.BookStats{
		"a": {Avg: 2, Count: 5000}, // bem abaixo da média dos candidatos
		"b": {Avg: 4.8, Count: 5000},
	})
	got := Recommend(globalData(), "me", 0, o).Recommendations
	pred := func(rs []Recommendation, id string) float64 {
		for _, x := range rs {
			if x.BookID == id {
				return x.Predicted
			}
		}
		t.Fatalf("%s sumiu", id)
		return 0
	}
	if pred(got, "a") >= pred(base, "a") || pred(got, "b") <= pred(base, "b") {
		t.Fatalf("a nota geral deveria puxar a previsão: a %.2f→%.2f, b %.2f→%.2f",
			pred(base, "a"), pred(got, "a"), pred(base, "b"), pred(got, "b"))
	}
}

// Sem Global (ou com pesos zerados), nada muda.
func TestGlobalOffIsNoop(t *testing.T) {
	o := opts()
	o.MinOverlap = 2
	o.GlobalBlend = 0 // o padrão liga a previsão pela nota geral
	want := Recommend(globalData(), "me", 0, o).Recommendations
	o.Global = statsOf(map[string]domain.BookStats{"a": {Avg: 1, Count: 9999}})
	got := Recommend(globalData(), "me", 0, o).Recommendations
	if len(got) != len(want) {
		t.Fatal("tamanho mudou")
	}
	for i := range got {
		// tolerância: somas em ordem de map variam no último bit
		if got[i].BookID != want[i].BookID || math.Abs(got[i].Score-want[i].Score) > 1e-9 || math.Abs(got[i].Predicted-want[i].Predicted) > 1e-9 {
			t.Fatalf("pesos zerados não deveriam mudar nada: %+v vs %+v", got[i], want[i])
		}
	}
}
