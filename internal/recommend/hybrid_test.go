package recommend

import (
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

func ids(recs []Recommendation) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.BookID
	}
	return out
}

func has(recs []Recommendation, id string) bool {
	for _, r := range recs {
		if r.BookID == id {
			return true
		}
	}
	return false
}

// O alvo não tem nenhum vizinho user-based (MinOverlap alto), mas quem amou
// "duna" e "neuro" também amou "fundacao": o item-based recomenda mesmo assim.
func TestItemBasedCoversWhenNoUserNeighbors(t *testing.T) {
	data := group(
		r("me", "duna", 5, 1), r("me", "neuro", 5, 1), r("me", "chato", 1, 1),
		r("a", "duna", 5, 0), r("a", "neuro", 4.5, 0), r("a", "fundacao", 5, 0), r("a", "chato", 1, 0), r("a", "romance", 2, 0),
		r("b", "duna", 4.5, 0), r("b", "neuro", 5, 0), r("b", "fundacao", 5, 0), r("b", "chato", 2, 0), r("b", "romance", 1, 0),
		r("c", "duna", 1, 0), r("c", "neuro", 2, 0), r("c", "romance", 5, 0), r("c", "chato", 5, 0), r("c", "fundacao", 2, 0),
	)
	o := opts()
	o.MinOverlap = 4
	o.ItemWeight = 0
	if res := Recommend(data, "me", 10, o); len(res.Recommendations) != 0 || len(res.Neighbors) != 0 {
		t.Fatalf("só user-based: esperava nada (sem vizinhos), veio %+v", res)
	}
	o.ItemWeight = 0.5
	res := Recommend(data, "me", 10, o)
	if len(res.Recommendations) == 0 || res.Recommendations[0].BookID != "fundacao" {
		t.Fatalf("híbrido deveria recomendar fundacao primeiro: %v", ids(res.Recommendations))
	}
	top := res.Recommendations[0]
	if top.ItemSupport != 2 || len(top.BecauseOf) != 2 {
		t.Fatalf("explicação item-based deveria citar duna e neuro (e não chato): %+v", top)
	}
	for _, e := range top.BecauseOf {
		if e.BookID == "chato" {
			t.Fatalf("livro de que você não gostou não explica: %+v", top.BecauseOf)
		}
	}
}

// Com α=0 o híbrido é exatamente o user-based antigo (mesma previsão).
func TestAlphaZeroMatchesUserBasedPrediction(t *testing.T) {
	data := group(
		r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 4, 0),
		r("v1", "a", 5, 0), r("v1", "b", 1, 0), r("v1", "c", 4, 0), r("v1", "x", 5, 0),
		r("v2", "a", 4, 0), r("v2", "b", 2, 0), r("v2", "c", 4, 0), r("v2", "x", 4, 0),
	)
	o := opts()
	o.ItemWeight = 0
	o.Ranking = RankPrediction
	res := Recommend(data, "me", 10, o)
	if len(res.Recommendations) != 1 {
		t.Fatalf("esperava só x: %+v", res.Recommendations)
	}
	// μme = 10/3; desvio em x: v1 5-15/4, v2 4-14/4 (nota menos a média de cada um).
	got := res.Recommendations[0]
	var num, den float64
	for _, nb := range res.Neighbors {
		dev := map[string]float64{"v1": 5 - 15.0/4, "v2": 4 - 14.0/4}[nb.UserID]
		num += nb.Sim * dev
		den += nb.Sim
	}
	want := clamp(10.0/3+num/den, 0.5, 5)
	if d := got.Predicted - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("previsão %v, esperava %v", got.Predicted, want)
	}
}

// Ranking por likes: um livro que muitos vizinhos gostaram vence um que só
// dois vizinhos amaram, mesmo com nota prevista um pouco menor.
func TestLikesRankingPrefersBroadEvidence(t *testing.T) {
	var rs []domain.Rating
	rs = append(rs, r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 4, 0), r("me", "d", 2, 0))
	for i, u := range []string{"n1", "n2", "n3", "n4", "n5", "n6"} {
		rs = append(rs, r(u, "a", 5, 0), r(u, "b", 1, 0), r(u, "c", 4, 0), r(u, "d", 2, 0), r(u, "amplo", 4, 0))
		if i < 2 {
			rs = append(rs, r(u, "nicho", 5, 0))
		}
	}
	o := opts()
	o.ItemWeight = 0
	o.Ranking = RankLikes
	if got := ids(Recommend(group(rs...), "me", 2, o).Recommendations); len(got) != 2 || got[0] != "amplo" {
		t.Fatalf("likes: amplo deveria vir primeiro, veio %v", got)
	}
	o.Ranking = RankPrediction
	if got := ids(Recommend(group(rs...), "me", 2, o).Recommendations); len(got) != 2 || got[0] != "nicho" {
		t.Fatalf("prediction: nicho (nota 5) deveria vir primeiro, veio %v", got)
	}
}

// Abandonar sem nota vira nota implícita baixa quando AbandonedStars > 0:
// o vizinho que abandonou o que você amou deixa de parecer com você.
func TestAbandonedAsImplicitNegative(t *testing.T) {
	ab := func(u, b string) domain.Rating {
		return domain.Rating{UserID: u, BookID: b, Status: domain.StatusAbandoned, ReadAt: now}
	}
	data := group(
		r("me", "a", 5, 0), r("me", "b", 5, 0), r("me", "c", 1, 0), r("me", "d", 2, 0),
		r("v", "a", 4, 0), ab("v", "b"), r("v", "c", 1, 0), r("v", "d", 1, 0),
	)
	o := opts()
	o.MinOverlap = 3
	o.AbandonedStars = 0
	without := Recommend(data, "me", 10, o).Neighbors
	o.AbandonedStars = 1
	with := Recommend(data, "me", 10, o).Neighbors
	if len(without) != 1 {
		t.Fatalf("sem sinal implícito, v é vizinho (3 notas em comum): %+v", without)
	}
	if len(with) == 1 && with[0].Sim >= without[0].Sim {
		t.Fatalf("abandono deveria reduzir a similaridade: %v -> %v", without[0].Sim, with[0].Sim)
	}
}

// Leituras sem data valem a mediana das datadas, não o peso máximo.
func TestUndatedReadsGetMedianWeight(t *testing.T) {
	rs := []domain.Rating{
		r("me", "new", 5, 0), r("me", "mid", 5, 365), r("me", "old", 5, 730),
		{UserID: "me", BookID: "nodate", Stars: 5},
	}
	o := opts()
	w := decayFunc(rs, o, now)
	if got := w(rs[3]); got < 0.49 || got > 0.51 {
		t.Fatalf("sem data deveria pesar a mediana (0.5), pesou %v", got)
	}
	o.HalfLife = 0
	if w := decayFunc(rs, o, now); w(rs[3]) != 1 || w(rs[2]) != 1 {
		t.Fatal("sem meia-vida, tudo pesa 1")
	}
	if w := decayFunc([]domain.Rating{{BookID: "x", Stars: 4}}, opts(), now); w(domain.Rating{}) != 1 {
		t.Fatal("sem nenhuma data, peso 1")
	}
}

func TestMaxPerAuthorDiversifies(t *testing.T) {
	var rs []domain.Rating
	rs = append(rs, r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 4, 0))
	for _, u := range []string{"n1", "n2", "n3"} {
		rs = append(rs, r(u, "a", 5, 0), r(u, "b", 1, 0), r(u, "c", 4, 0),
			r(u, "k1", 5, 0), r(u, "k2", 5, 0), r(u, "k3", 5, 0), r(u, "outro", 4, 0))
	}
	author := map[string]string{"k1": "King", "k2": "King ", "k3": "king"}
	o := opts()
	o.MaxPerAuthor = 2
	o.AuthorOf = func(id string) string { return author[id] }
	got := Recommend(group(rs...), "me", 3, o).Recommendations
	kings := 0
	for _, x := range got {
		if author[x.BookID] != "" {
			kings++
		}
	}
	if len(got) != 3 || kings != 2 || !has(got, "outro") {
		t.Fatalf("esperava 2 do King + outro: %v", ids(got))
	}
}

func TestPopularBaseline(t *testing.T) {
	data := group(
		r("me", "lido", 5, 0),
		r("a", "hit", 5, 0), r("b", "hit", 4, 0), r("c", "hit", 4.5, 0), r("a", "lido", 5, 0),
		r("a", "meh", 3, 0), r("b", "meh", 3, 0), r("c", "meh", 3, 0), r("d", "meh", 3, 0),
	)
	got := Popular(data, "me", 0)
	if len(got) != 2 || got[0].BookID != "hit" || has(got, "lido") {
		t.Fatalf("popularidade conta quem gostou e ignora lidos: %+v", got)
	}
	if got[0].Predicted != 4.5 {
		t.Fatalf("previsão = média: %v", got[0].Predicted)
	}
}
