// Package recommend implementa filtragem colaborativa híbrida: KNN por
// usuário (user-based) combinado com KNN por item (item-based), com pesos
// temporais, shrinkage e sinais implícitos.
//
// User-based, para o alvo u e um vizinho v:
//
//	w_i      = 0.5^(idade_i / HalfLife)                     // leituras recentes do alvo pesam mais
//	sim(u,v) = Σ w_i·(r_ui-μu)(r_vi-μv) / √(Σ w_i(r_ui-μu)² · Σ w_i(r_vi-μv)²)   // Pearson ponderado
//	sim'     = (sim · n/(n+Shrinkage))^Amplify              // shrinkage + case amplification
//	r̂ᵁ_uj    = μu + Σ_v sim'(u,v)·(r_vj-μv) / Σ_v sim'(u,v)  (top-K vizinhos com sim' > 0)
//
// Item-based, para um candidato j e um livro i avaliado pelo alvo
// (cosseno ajustado: notas centradas na média de cada leitor):
//
//	sim(i,j) = Σ_v (r_vi-μv)(r_vj-μv) / √(Σ_v (r_vi-μv)² · Σ_v (r_vj-μv)²) · c/(c+ItemShrinkage)
//	r̂ᴵ_uj    = μu + Σ_i sim(i,j)·w_i·(r_ui-μu) / Σ_i sim(i,j)·w_i   (top-ItemK livros do alvo)
//
// Mistura: cada previsão entra com peso α (ItemWeight) ou 1-α, vezes a sua
// confiança c = Σsim/(Σsim+1). O score de ordenação puxa a previsão para μu
// quando a evidência somada é fraca: score = μu + (r̂-μu)·Σpesos.
//
// Notas centradas na média removem o viés de quem dá 5 para tudo.
package recommend

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

type Options struct {
	HalfLife   time.Duration // meia-vida do peso temporal; 0 desliga
	Shrinkage  float64       // β do shrinkage user-user
	K          int           // número de vizinhos
	MinOverlap int           // mínimo de livros avaliados em comum
	MinSupport int           // mínimo de vizinhos (ou de livros seus similares) por candidato
	Amplify    float64       // ρ: eleva a similaridade user-user (1 = desliga); realça os vizinhos mais parecidos

	// AbandonedStars é a nota implícita de um livro abandonado sem nota
	// (0 = ignora). Abandonar é um sinal negativo forte, para você e para os vizinhos.
	AbandonedStars float64

	ItemWeight    float64 // α: 0 = só user-based, 1 = só item-based
	ItemK         int     // livros seus mais similares usados por candidato
	ItemShrinkage float64 // shrinkage da similaridade item-item

	// Diversidade: no máximo MaxPerAuthor livros do mesmo autor no top-n
	// (0 = sem limite). AuthorOf diz o autor de um livro.
	MaxPerAuthor int
	AuthorOf     func(bookID string) string

	// Ranking define a ordem do top-n (a nota prevista é a mesma em todos):
	// RankPrediction, RankLikes ou RankDeviation.
	Ranking string
	// PopWeight mistura a popularidade (quantos no grafo gostaram) no
	// ranking por evidência: 0 = só personalização.
	PopWeight float64

	Now time.Time // referência para idade (testes)
}

const (
	// RankPrediction ordena pela nota prevista, encolhida para a média
	// quando a evidência é fraca. Bom para estimar notas, fraco para top-n:
	// favorece livros de nicho que 2 ou 3 vizinhos amaram.
	RankPrediction = "prediction"
	// RankLikes soma a similaridade de quem gostou (nota >= 4): vizinhos
	// parecidos que gostaram do livro e livros seus de que você gostou
	// parecidos com ele. Premia evidência ampla.
	RankLikes = "likes"
	// RankDeviation soma similaridade × desvio da média (o numerador da
	// previsão, sem dividir pelo total de evidência).
	RankDeviation = "deviation"
)

// gain é quanto uma nota conta como evidência a favor, conforme o ranking.
func gain(ranking string, stars, dev float64) float64 {
	switch ranking {
	case RankDeviation:
		return dev
	default:
		if stars >= LikeThreshold {
			return 1
		}
		return 0
	}
}

// DefaultOptions são os valores escolhidos com `recomenda eval` sobre dados
// reais do Skoob (137 leitores, cortes aleatórios e temporal; ver README):
//   - Ranking por likes bate a popularidade; por nota prevista, não.
//   - α=0,5 foi o mais estável entre cortes; 0,75 só ganhava em amostra pequena.
//   - Abandono implícito piorou o MAE em todos os cortes: fica desligado.
//   - Meia-vida de 1 ano é a melhor no corte temporal (o cenário real).
//   - Popularidade como prior (PopWeight) não ajudou de forma consistente.
func DefaultOptions() Options {
	return Options{
		HalfLife: 365 * 24 * time.Hour, Shrinkage: 5, K: 50, MinOverlap: 3, MinSupport: 2, Amplify: 1,
		ItemWeight: 0.5, ItemK: 30, ItemShrinkage: 10,
		Ranking:      RankLikes,
		MaxPerAuthor: 3,
	}
}

type Neighbor struct {
	UserID  string
	Sim     float64
	Overlap int
}

type Recommendation struct {
	BookID      string
	Predicted   float64 // nota prevista (0.5..5)
	Score       float64 // ordenação: previsão ajustada pela confiança
	Support     int     // vizinhos que avaliaram
	ItemSupport int     // livros seus similares a este
	Because     []Evidence
	BecauseOf   []BookEvidence
}

// Evidence explica a recomendação: "vizinho X (sim 0.82) deu 5".
type Evidence struct {
	UserID string
	Sim    float64
	Stars  float64
	Dev    float64 // nota do vizinho menos a média dele (contribuição = Sim·Dev)
}

// BookEvidence explica pelo lado item-based: "porque você deu 5 a X".
type BookEvidence struct {
	BookID string
	Sim    float64 // similaridade entre X e o recomendado
	Stars  float64 // sua nota para X
}

type Result struct {
	Mean            float64 // média (ponderada) das notas do alvo
	Neighbors       []Neighbor
	Recommendations []Recommendation
}

type userIndex struct {
	mean  float64
	stars map[string]float64 // notas explícitas + implícitas (abandonei)
	read  map[string]bool    // lidos/abandonados, com ou sem nota (não recomendar)
}

func index(rs []domain.Rating, weight func(domain.Rating) float64, abandoned float64) userIndex {
	ix := userIndex{stars: map[string]float64{}, read: map[string]bool{}}
	var sum, wsum float64
	for _, r := range rs {
		if r.Consumed() {
			ix.read[r.BookID] = true
		}
		s, ok := stars(r, abandoned)
		if !ok {
			continue
		}
		ix.stars[r.BookID] = s
		w := weight(r)
		sum += w * s
		wsum += w
	}
	if wsum > 0 {
		ix.mean = sum / wsum
	}
	return ix
}

// stars devolve a nota usada pelo algoritmo: a explícita, ou a implícita
// de um abandono sem nota.
func stars(r domain.Rating, abandoned float64) (float64, bool) {
	if r.Rated() {
		return r.Stars, true
	}
	if abandoned > 0 && r.Status == domain.StatusAbandoned {
		return abandoned, true
	}
	return 0, false
}

// decayFunc devolve o peso temporal das leituras do alvo. Leituras sem data
// recebem a mediana dos pesos das datadas: dar peso 1 (o máximo) faria
// leituras antigas sem data valerem mais que as do mês passado.
func decayFunc(rs []domain.Rating, o Options, now time.Time) func(domain.Rating) float64 {
	raw := func(t time.Time) float64 {
		age := max(now.Sub(t), 0)
		return math.Pow(0.5, float64(age)/float64(o.HalfLife))
	}
	if o.HalfLife <= 0 {
		return func(domain.Rating) float64 { return 1 }
	}
	var dated []float64
	for _, r := range rs {
		if !r.ReadAt.IsZero() {
			dated = append(dated, raw(r.ReadAt))
		}
	}
	undated := 1.0
	if len(dated) > 0 {
		sort.Float64s(dated)
		undated = dated[len(dated)/2]
		if len(dated)%2 == 0 {
			undated = (dated[len(dated)/2-1] + dated[len(dated)/2]) / 2
		}
	}
	return func(r domain.Rating) float64 {
		if r.ReadAt.IsZero() {
			return undated
		}
		return raw(r.ReadAt)
	}
}

type candidate struct {
	uNum, uDen float64
	uEv        []Evidence
	links      []itemLink
}

type itemLink struct {
	from string  // livro do alvo
	sim  float64 // sim(from, candidato)
}

// Recommend gera as top-n recomendações para target a partir de ratings
// (userID -> leituras). n <= 0 devolve todos os candidatos.
func Recommend(ratings map[string][]domain.Rating, target string, n int, o Options) Result {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	decay := decayFunc(ratings[target], o, now)
	one := func(domain.Rating) float64 { return 1 }

	me := index(ratings[target], decay, o.AbandonedStars)
	wOf := map[string]float64{}
	for _, r := range ratings[target] {
		wOf[r.BookID] = decay(r)
	}

	idx := make(map[string]userIndex, len(ratings))
	for uid, rs := range ratings {
		if uid != target {
			idx[uid] = index(rs, one, o.AbandonedStars)
		}
	}

	neighbors := userNeighbors(me, idx, wOf, o)
	cand := map[string]*candidate{}
	get := func(b string) *candidate {
		c := cand[b]
		if c == nil {
			c = &candidate{}
			cand[b] = c
		}
		return c
	}

	if o.ItemWeight < 1 {
		for _, nb := range neighbors {
			v := idx[nb.UserID]
			for b, rv := range v.stars {
				if me.read[b] {
					continue
				}
				c := get(b)
				dev := rv - v.mean
				c.uNum += nb.Sim * dev
				c.uDen += nb.Sim
				c.uEv = append(c.uEv, Evidence{UserID: nb.UserID, Sim: nb.Sim, Stars: rv, Dev: dev})
			}
		}
	}
	if o.ItemWeight > 0 {
		itemLinks(me, idx, o, get)
	}

	alpha := clamp(o.ItemWeight, 0, 1)
	minSup := max(o.MinSupport, 1)
	recs := make([]Recommendation, 0, len(cand))
	type evid struct{ u, i float64 } // evidência bruta para o ranking
	ev := make([]evid, 0, len(cand))
	var maxU, maxI float64
	for b, c := range cand {
		rec := Recommendation{BookID: b, Support: len(c.uEv)}
		var wsum, psum float64
		var e evid
		if len(c.uEv) >= minSup && c.uDen > 0 {
			pred := clamp(me.mean+c.uNum/c.uDen, 0.5, 5)
			w := (1 - alpha) * c.uDen / (c.uDen + 1)
			wsum += w
			psum += w * pred
			for _, x := range c.uEv {
				e.u += x.Sim * gain(o.Ranking, x.Stars, x.Dev)
			}
			rec.Because = topEvidence(c.uEv)
		}
		if len(c.links) > 0 {
			sort.Slice(c.links, func(i, j int) bool {
				if c.links[i].sim != c.links[j].sim {
					return c.links[i].sim > c.links[j].sim
				}
				return c.links[i].from < c.links[j].from
			})
			if o.ItemK > 0 && len(c.links) > o.ItemK {
				c.links = c.links[:o.ItemK]
			}
			rec.ItemSupport = len(c.links)
		}
		if rec.ItemSupport >= minSup {
			var num, den float64
			for _, l := range c.links {
				w := l.sim * wOf[l.from]
				s := me.stars[l.from]
				num += w * (s - me.mean)
				den += w
				e.i += w * gain(o.Ranking, s, s-me.mean)
			}
			if den > 0 {
				pred := clamp(me.mean+num/den, 0.5, 5)
				w := alpha * den / (den + 1)
				wsum += w
				psum += w * pred
				rec.BecauseOf = topBooks(c.links, me)
			}
		}
		if wsum == 0 {
			continue
		}
		rec.Predicted = psum / wsum
		rec.Score = me.mean + (rec.Predicted-me.mean)*wsum
		recs = append(recs, rec)
		ev = append(ev, e)
		maxU, maxI = max(maxU, e.u), max(maxI, e.i)
	}
	// Ranking por evidência: soma (sem normalizar) do quanto vizinhos e
	// livros similares apontam para o candidato. Cada lado é escalado pelo
	// seu máximo para que α misture grandezas comparáveis.
	if o.Ranking != "" && o.Ranking != RankPrediction {
		var pop map[string]int
		maxPop := 0
		if o.PopWeight > 0 {
			pop = likers(idx)
			for _, rec := range recs {
				maxPop = max(maxPop, pop[rec.BookID])
			}
		}
		for k := range recs {
			var s float64
			if maxU > 0 {
				s += (1 - alpha) * ev[k].u / maxU
			}
			if maxI > 0 {
				s += alpha * ev[k].i / maxI
			}
			if maxPop > 0 {
				s = (1-o.PopWeight)*s + o.PopWeight*float64(pop[recs[k].BookID])/float64(maxPop)
			}
			recs[k].Score = s
		}
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Score != recs[j].Score {
			return recs[i].Score > recs[j].Score
		}
		return recs[i].BookID < recs[j].BookID
	})
	recs = diversify(recs, n, o)
	return Result{Mean: me.mean, Neighbors: neighbors, Recommendations: recs}
}

// userNeighbors calcula o Pearson ponderado contra cada usuário e devolve
// os top-K com similaridade positiva.
func userNeighbors(me userIndex, idx map[string]userIndex, wOf map[string]float64, o Options) []Neighbor {
	var neighbors []Neighbor
	for uid, v := range idx {
		var num, du, dv float64
		overlap := 0
		for b, ru := range me.stars {
			rv, ok := v.stars[b]
			if !ok {
				continue
			}
			overlap++
			w := wOf[b]
			a, c := ru-me.mean, rv-v.mean
			num += w * a * c
			du += w * a * a
			dv += w * c * c
		}
		if overlap < o.MinOverlap || du == 0 || dv == 0 {
			continue
		}
		sim := num / math.Sqrt(du*dv)
		sim *= float64(overlap) / (float64(overlap) + o.Shrinkage)
		if sim <= 0 {
			continue
		}
		if o.Amplify > 0 && o.Amplify != 1 {
			sim = math.Pow(sim, o.Amplify)
		}
		neighbors = append(neighbors, Neighbor{UserID: uid, Sim: sim, Overlap: overlap})
	}
	sort.Slice(neighbors, func(i, j int) bool {
		if neighbors[i].Sim != neighbors[j].Sim {
			return neighbors[i].Sim > neighbors[j].Sim
		}
		return neighbors[i].UserID < neighbors[j].UserID
	})
	if o.K > 0 && len(neighbors) > o.K {
		neighbors = neighbors[:o.K]
	}
	return neighbors
}

// itemLinks calcula o cosseno ajustado entre cada livro avaliado pelo alvo e
// cada candidato, só para os pares que têm leitores em comum. O custo é
// Σ_i Σ_{v leu i} |estante de v|, não |livros|².
func itemLinks(me userIndex, idx map[string]userIndex, o Options, get func(string) *candidate) {
	raters := map[string][]string{} // livro -> quem deu nota (exceto o alvo)
	norm := map[string]float64{}    // livro -> Σ (r_vj-μv)²
	for uid, v := range idx {
		for b, s := range v.stars {
			d := s - v.mean
			norm[b] += d * d
			if _, mine := me.stars[b]; mine {
				raters[b] = append(raters[b], uid)
			}
		}
	}
	minCo := max(o.MinSupport, 2)
	type acc struct {
		dot float64
		co  int
	}
	dots := map[string]*acc{}
	for i := range me.stars {
		if norm[i] == 0 {
			continue
		}
		clear(dots)
		for _, uid := range raters[i] {
			v := idx[uid]
			ci := v.stars[i] - v.mean
			if ci == 0 {
				continue
			}
			for j, s := range v.stars {
				if me.read[j] {
					continue
				}
				a := dots[j]
				if a == nil {
					a = &acc{}
					dots[j] = a
				}
				a.dot += ci * (s - v.mean)
				a.co++
			}
		}
		for j, a := range dots {
			if a.co < minCo || a.dot <= 0 || norm[j] == 0 {
				continue
			}
			sim := a.dot / math.Sqrt(norm[i]*norm[j]) * float64(a.co) / (float64(a.co) + o.ItemShrinkage)
			c := get(j)
			c.links = append(c.links, itemLink{from: i, sim: sim})
		}
	}
}

// likers conta, por livro, quantos usuários (fora o alvo) deram nota >= LikeThreshold.
func likers(idx map[string]userIndex) map[string]int {
	out := map[string]int{}
	for _, v := range idx {
		for b, s := range v.stars {
			if s >= LikeThreshold {
				out[b]++
			}
		}
	}
	return out
}

// topEvidence: só quem puxou a previsão para cima, maiores contribuições primeiro.
func topEvidence(ev []Evidence) []Evidence {
	sort.Slice(ev, func(i, j int) bool { return ev[i].Sim*ev[i].Dev > ev[j].Sim*ev[j].Dev })
	var top []Evidence
	for _, e := range ev {
		if e.Dev > 0 && len(top) < 3 {
			top = append(top, e)
		}
	}
	return top
}

// topBooks: livros seus acima da sua média que mais pesaram (até 3).
func topBooks(links []itemLink, me userIndex) []BookEvidence {
	var out []BookEvidence
	for _, l := range links { // já ordenados por similaridade
		if s := me.stars[l.from]; s > me.mean && len(out) < 3 {
			out = append(out, BookEvidence{BookID: l.from, Sim: l.sim, Stars: s})
		}
	}
	return out
}

// diversify corta a lista em n respeitando MaxPerAuthor (livros sem autor
// conhecido não contam). n <= 0 devolve a lista inteira, sem corte.
func diversify(recs []Recommendation, n int, o Options) []Recommendation {
	if n <= 0 {
		return recs
	}
	if o.MaxPerAuthor <= 0 || o.AuthorOf == nil {
		if len(recs) > n {
			recs = recs[:n]
		}
		return recs
	}
	out := make([]Recommendation, 0, n)
	per := map[string]int{}
	for _, r := range recs {
		if len(out) == n {
			break
		}
		if a := strings.ToLower(strings.TrimSpace(o.AuthorOf(r.BookID))); a != "" {
			if per[a] >= o.MaxPerAuthor {
				continue
			}
			per[a]++
		}
		out = append(out, r)
	}
	return out
}

// Popular é o baseline de popularidade: livros que mais leitores (fora o
// alvo) gostaram (nota >= LikeThreshold), com a nota média como previsão.
// Serve de referência em `recomenda eval`: um algoritmo personalizado que
// não bate isso não está aprendendo nada sobre o seu gosto.
func Popular(ratings map[string][]domain.Rating, target string, n int) []Recommendation {
	read := map[string]bool{}
	for _, r := range ratings[target] {
		if r.Consumed() {
			read[r.BookID] = true
		}
	}
	likes, sum, cnt := map[string]int{}, map[string]float64{}, map[string]int{}
	for uid, rs := range ratings {
		if uid == target {
			continue
		}
		for _, r := range rs {
			if !r.Rated() || read[r.BookID] {
				continue
			}
			sum[r.BookID] += r.Stars
			cnt[r.BookID]++
			if r.Stars >= LikeThreshold {
				likes[r.BookID]++
			}
		}
	}
	recs := make([]Recommendation, 0, len(cnt))
	for b, c := range cnt {
		recs = append(recs, Recommendation{BookID: b, Predicted: sum[b] / float64(c), Score: float64(likes[b]), Support: c})
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Score != recs[j].Score {
			return recs[i].Score > recs[j].Score
		}
		return recs[i].BookID < recs[j].BookID
	})
	if n > 0 && len(recs) > n {
		recs = recs[:n]
	}
	return recs
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }
