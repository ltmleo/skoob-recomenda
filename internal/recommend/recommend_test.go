package recommend

import (
	"testing"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

var now = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func r(u, b string, s float64, daysAgo int) domain.Rating {
	return domain.Rating{UserID: u, BookID: b, Stars: s, ReadAt: now.AddDate(0, 0, -daysAgo)}
}

func opts() Options {
	o := DefaultOptions()
	o.Now = now
	return o
}

func group(rs ...domain.Rating) map[string][]domain.Rating {
	m := map[string][]domain.Rating{}
	for _, x := range rs {
		m[x.UserID] = append(m[x.UserID], x)
	}
	return m
}

// Vizinhos com gosto igual ao alvo amam "duna"; vizinhos de gosto oposto amam "romance".
func TestRecommendsWhatSimilarUsersLike(t *testing.T) {
	data := group(
		r("me", "a", 5, 10), r("me", "b", 4, 20), r("me", "c", 1, 30), r("me", "d", 2, 40),
		r("twin1", "a", 5, 0), r("twin1", "b", 4, 0), r("twin1", "c", 1, 0), r("twin1", "d", 2, 0), r("twin1", "duna", 5, 0), r("twin1", "romance", 1, 0),
		r("twin2", "a", 4.5, 0), r("twin2", "b", 4, 0), r("twin2", "c", 1.5, 0), r("twin2", "d", 1, 0), r("twin2", "duna", 5, 0), r("twin2", "romance", 2, 0),
		r("anti", "a", 1, 0), r("anti", "b", 2, 0), r("anti", "c", 5, 0), r("anti", "d", 5, 0), r("anti", "romance", 5, 0), r("anti", "duna", 1, 0),
	)
	res := Recommend(data, "me", 10, opts())
	if len(res.Recommendations) == 0 || res.Recommendations[0].BookID != "duna" {
		t.Fatalf("esperava duna em 1º, veio %+v", res.Recommendations)
	}
	for _, n := range res.Neighbors {
		if n.UserID == "anti" {
			t.Fatalf("vizinho com correlação negativa não deveria entrar: %+v", n)
		}
	}
	for _, rec := range res.Recommendations {
		if rec.BookID == "a" {
			t.Fatal("recomendou livro já lido")
		}
	}
}

// Dois vizinhos concordam com o alvo em livros diferentes: um nos livros recentes,
// outro nos antigos. Com decaimento temporal, o dos recentes deve ser mais similar.
func TestRecentReadsWeighMore(t *testing.T) {
	data := group(
		r("me", "new1", 5, 1), r("me", "new2", 1, 1), r("me", "old1", 5, 2000), r("me", "old2", 1, 2000),
		r("recent", "new1", 5, 0), r("recent", "new2", 1, 0), r("recent", "old1", 1, 0), r("recent", "old2", 5, 0),
		r("past", "new1", 1, 0), r("past", "new2", 5, 0), r("past", "old1", 5, 0), r("past", "old2", 1, 0),
	)
	o := opts()
	o.MinOverlap = 2
	res := Recommend(data, "me", 10, o)
	if len(res.Neighbors) != 1 || res.Neighbors[0].UserID != "recent" {
		t.Fatalf("esperava só 'recent' como vizinho positivo, veio %+v", res.Neighbors)
	}
	o.HalfLife = 0 // sem decaimento os dois são simétricos (sim = 0) e somem
	if res := Recommend(data, "me", 10, o); len(res.Neighbors) != 0 {
		t.Fatalf("sem decaimento esperava 0 vizinhos, veio %+v", res.Neighbors)
	}
}

// Shrinkage: com mesma correlação perfeita, mais livros em comum => maior similaridade.
func TestShrinkageFavorsLargerOverlap(t *testing.T) {
	data := group(
		r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 5, 0), r("me", "d", 1, 0), r("me", "e", 5, 0), r("me", "f", 1, 0),
		r("few", "a", 5, 0), r("few", "b", 1, 0), r("few", "c", 5, 0),
		r("many", "a", 5, 0), r("many", "b", 1, 0), r("many", "c", 5, 0), r("many", "d", 1, 0), r("many", "e", 5, 0), r("many", "f", 1, 0),
	)
	res := Recommend(data, "me", 10, opts())
	if len(res.Neighbors) != 2 || res.Neighbors[0].UserID != "many" || res.Neighbors[0].Sim <= res.Neighbors[1].Sim {
		t.Fatalf("esperava 'many' > 'few', veio %+v", res.Neighbors)
	}
}

func TestMinSupportFiltersSingleVoice(t *testing.T) {
	data := group(
		r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 4, 0),
		r("v1", "a", 5, 0), r("v1", "b", 1, 0), r("v1", "c", 4, 0), r("v1", "solo", 5, 0),
	)
	if res := Recommend(data, "me", 10, opts()); len(res.Recommendations) != 0 {
		t.Fatalf("livro com 1 só voto não deveria passar MinSupport=2: %+v", res.Recommendations)
	}
}

func TestUnratedReadIsNotRecommendedAndIgnoredInSimilarity(t *testing.T) {
	data := group(
		r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 4, 0), r("me", "x", 0, 0),
		r("v1", "a", 5, 0), r("v1", "b", 1, 0), r("v1", "c", 4, 0), r("v1", "x", 5, 0),
		r("v2", "a", 5, 0), r("v2", "b", 1, 0), r("v2", "c", 4, 0), r("v2", "x", 5, 0),
	)
	for _, rec := range Recommend(data, "me", 10, opts()).Recommendations {
		if rec.BookID == "x" {
			t.Fatal("livro lido sem nota foi recomendado")
		}
	}
}

func TestEvidenceOnlyListsPositiveContributors(t *testing.T) {
	data := group(
		r("me", "a", 5, 0), r("me", "b", 1, 0), r("me", "c", 4, 0),
		r("v1", "a", 5, 0), r("v1", "b", 1, 0), r("v1", "c", 4, 0), r("v1", "x", 5, 0),
		r("v2", "a", 5, 0), r("v2", "b", 1, 0), r("v2", "c", 4, 0), r("v2", "x", 1, 0),
	)
	for _, rec := range Recommend(data, "me", 10, opts()).Recommendations {
		for _, e := range rec.Because {
			if e.Dev <= 0 {
				t.Fatalf("evidência negativa listada como motivo: %+v", e)
			}
		}
		if rec.BookID == "x" && rec.Support != 2 {
			t.Fatalf("support deve contar todos os vizinhos, veio %d", rec.Support)
		}
	}
}

func TestWantToReadIsStillRecommendable(t *testing.T) {
	want := domain.Rating{UserID: "me", BookID: "duna", Status: domain.StatusWantToRead}
	rs := group(
		r("me", "a", 5, 1), r("me", "b", 1, 1), r("me", "c", 4, 1), want,
		r("v1", "a", 5, 1), r("v1", "b", 1, 1), r("v1", "c", 4, 1), r("v1", "duna", 5, 1),
		r("v2", "a", 5, 1), r("v2", "b", 2, 1), r("v2", "c", 4, 1), r("v2", "duna", 5, 1),
	)
	res := Recommend(rs, "me", 10, opts())
	found := false
	for _, x := range res.Recommendations {
		found = found || x.BookID == "duna"
	}
	if !found {
		t.Fatalf("livro em 'quero ler' deveria poder ser recomendado: %+v", res.Recommendations)
	}
	ab := domain.Rating{UserID: "me", BookID: "duna", Status: domain.StatusAbandoned}
	rs["me"][3] = ab
	for _, x := range Recommend(rs, "me", 10, opts()).Recommendations {
		if x.BookID == "duna" {
			t.Fatal("livro abandonado não deve ser recomendado")
		}
	}
}

func TestSimilarFollowsLikers(t *testing.T) {
	rs := group(
		r("a", "duna", 5, 0), r("a", "fundacao", 5, 0), r("a", "pop", 4, 0),
		r("b", "duna", 4.5, 0), r("b", "fundacao", 4, 0), r("b", "pop", 4, 0),
		r("c", "duna", 2, 0), r("c", "romance", 5, 0),
		r("d", "pop", 5, 0), r("e", "pop", 5, 0), r("f", "pop", 5, 0),
	)
	res := Similar(rs, "duna", 10, 2, 2)
	if res.Likers != 2 || res.Raters != 3 {
		t.Fatalf("contagens: %+v", res)
	}
	if len(res.Similar) != 2 || res.Similar[0].BookID != "fundacao" || res.Similar[1].BookID != "pop" {
		t.Fatalf("fundacao (nicho) deve vir antes de pop (popular); romance fica de fora: %+v", res.Similar)
	}
	if res.Similar[0].CoLikes != 2 {
		t.Fatalf("co-likes: %+v", res.Similar[0])
	}
}
