package recommend

import (
	"math"
	"sort"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// LikeThreshold é a nota a partir da qual alguém "gostou" de um livro.
const LikeThreshold = 4.0

// SimilarBook é um livro que quem gostou do livro-base também gostou.
type SimilarBook struct {
	BookID  string
	Score   float64 // cosseno entre os conjuntos de quem gostou, com shrinkage
	CoLikes int     // gostaram dos dois
	Likers  int     // gostaram deste livro (no total)
}

// SimilarResult descreve a vizinhança de um livro no grafo.
type SimilarResult struct {
	BookID  string
	Likers  int // gostaram do livro-base
	Raters  int // deram nota ao livro-base
	Similar []SimilarBook
}

// Similar segue o grafo a partir de um livro: livro -> quem gostou dele ->
// o que mais essas pessoas gostaram. A similaridade é o cosseno entre os
// conjuntos de leitores que deram nota >= LikeThreshold:
//
//	sim(A,B) = |L_A ∩ L_B| / √(|L_A|·|L_B|) · c/(c+shrink)
//
// O cosseno desconta livros que todo mundo gosta (popularidade), e o
// shrinkage evita que 1 leitor em comum gere similaridade alta.
func Similar(ratings map[string][]domain.Rating, bookID string, n, minCoLikes int, shrink float64) SimilarResult {
	likers := map[string][]string{} // livro -> usuários que gostaram
	res := SimilarResult{BookID: bookID}
	for uid, rs := range ratings {
		for _, r := range rs {
			if !r.Rated() {
				continue
			}
			if r.BookID == bookID {
				res.Raters++
			}
			if r.Stars >= LikeThreshold {
				likers[r.BookID] = append(likers[r.BookID], uid)
			}
		}
	}
	base := map[string]bool{}
	for _, u := range likers[bookID] {
		base[u] = true
	}
	res.Likers = len(base)
	if len(base) == 0 {
		return res
	}
	co := map[string]int{}
	for _, u := range likers[bookID] {
		for _, r := range ratings[u] {
			if r.BookID != bookID && r.Stars >= LikeThreshold {
				co[r.BookID]++
			}
		}
	}
	for b, c := range co {
		if c < minCoLikes {
			continue
		}
		lb := len(likers[b])
		s := float64(c) / math.Sqrt(float64(len(base))*float64(lb)) * float64(c) / (float64(c) + shrink)
		res.Similar = append(res.Similar, SimilarBook{BookID: b, Score: s, CoLikes: c, Likers: lb})
	}
	sort.Slice(res.Similar, func(i, j int) bool {
		if res.Similar[i].Score != res.Similar[j].Score {
			return res.Similar[i].Score > res.Similar[j].Score
		}
		return res.Similar[i].BookID < res.Similar[j].BookID
	})
	if n > 0 && len(res.Similar) > n {
		res.Similar = res.Similar[:n]
	}
	return res
}
