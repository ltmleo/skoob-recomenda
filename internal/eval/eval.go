// Package eval mede a qualidade das recomendações offline: esconde parte das
// notas de alguns leitores, recomenda com o resto e confere se os livros
// escondidos de que eles gostaram aparecem no topo.
//
//	Precision@K = acertos no top-K / K
//	Recall@K    = acertos no top-K / livros escondidos com nota >= 4
//	nDCG@K      = ganho descontado pela posição (acertar no 1º vale mais que no 20º)
//	MAE / RMSE  = erro da nota prevista nos livros escondidos que receberam previsão
//	Cobertura   = fração dos escondidos para os quais o algoritmo previu algo
//
// Com poucas centenas de leitores, a diferença entre duas variantes costuma
// ser menor que o ruído do sorteio. Por isso Run aceita várias sementes e
// Compare dá o intervalo de confiança (bootstrap pareado por leitor) da
// diferença de nDCG: só conte como ganho o que não cruza o zero.
package eval

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
)

// Config controla o protocolo de avaliação.
type Config struct {
	Users      int     // quantos leitores testar (além de Target)
	Target     string  // sempre testado, se tiver notas suficientes (opcional)
	MinRatings int     // só testa quem tem pelo menos isso de notas
	Holdout    float64 // fração das notas escondida por leitor
	At         int     // K das métricas @K
	// Temporal esconde as leituras mais recentes em vez de sorteá-las. Os
	// outros leitores também perdem o que leram depois da data de corte:
	// senão os vizinhos "já sabem" o futuro.
	Temporal bool
	Seed     int64
	Seeds    int // repete o sorteio com Seed, Seed+1, ... (corte temporal: 1 basta)
}

func DefaultConfig() Config {
	return Config{Users: 40, MinRatings: 20, Holdout: 0.2, At: 20, Seed: 1, Seeds: 1}
}

// Variant é um algoritmo a comparar. Rec devolve a lista completa ordenada;
// hidden são as notas escondidas do leitor (só para descontar vazamentos,
// como a nota dele embutida na média geral do livro; nunca para recomendar).
type Variant struct {
	Name string
	Rec  func(ratings map[string][]domain.Rating, user string, hidden []domain.Rating) []recommend.Recommendation
}

// KNN embrulha recommend.Recommend com as opções dadas. Se houver nota
// geral (o.Global), a nota escondida do leitor é retirada da média do livro.
func KNN(name string, o recommend.Options) Variant {
	o.MaxPerAuthor = 0 // diversidade é apresentação; não entra na métrica
	return Variant{Name: name, Rec: func(rs map[string][]domain.Rating, u string, hidden []domain.Rating) []recommend.Recommendation {
		x := o
		if o.Global != nil {
			x.Global = withoutUser(o.Global, hidden)
		}
		return recommend.Recommend(rs, u, 0, x).Recommendations
	}}
}

// withoutUser tira as notas dadas do leitor da média geral de cada livro:
// avg' = (avg·n − r)/(n − 1).
func withoutUser(g func(string) (domain.BookStats, bool), hidden []domain.Rating) func(string) (domain.BookStats, bool) {
	mine := map[string]float64{}
	for _, h := range hidden {
		if h.Rated() {
			mine[h.BookID] = h.Stars
		}
	}
	return func(b string) (domain.BookStats, bool) {
		st, ok := g(b)
		r, rated := mine[b]
		if !ok || !rated {
			return st, ok
		}
		if st.Count <= 1 {
			return domain.BookStats{}, false
		}
		st.Avg = (st.Avg*float64(st.Count) - r) / float64(st.Count-1)
		st.Count--
		return st, true
	}
}

// PopularVariant é o baseline de popularidade.
func PopularVariant() Variant {
	return Variant{Name: "popularidade", Rec: func(rs map[string][]domain.Rating, u string, _ []domain.Rating) []recommend.Recommendation {
		return recommend.Popular(rs, u, 0)
	}}
}

// Metrics resume uma variante (médias por leitor testado, somando sementes).
type Metrics struct {
	Name                    string
	Users                   int // leitores com algum livro relevante escondido (× sementes)
	Precision, Recall, NDCG float64
	MAE, RMSE, Coverage     float64
	// NDCGStd é o desvio-padrão do nDCG entre sementes (0 com uma semente).
	NDCGStd float64
	// PerUser é o nDCG de cada (semente, leitor), na mesma ordem em todas
	// as variantes de um Run: é o que Compare pareia.
	PerUser []float64

	predicted, hidden, sqErr, absErr float64
}

type split struct {
	seed   int
	user   string
	train  map[string][]domain.Rating
	hidden []domain.Rating
}

// Run avalia todas as variantes sobre os mesmos cortes.
func Run(ratings map[string][]domain.Rating, cfg Config, variants ...Variant) []Metrics {
	seeds := max(cfg.Seeds, 1)
	if cfg.Temporal && cfg.Users < 0 {
		seeds = 1 // o corte temporal com todos os leitores é determinístico
	}
	var splits []split
	for s := range seeds {
		c := cfg
		c.Seed = cfg.Seed + int64(s)
		for _, sp := range makeSplits(ratings, c) {
			sp.seed = s
			splits = append(splits, sp)
		}
	}
	out := make([]Metrics, len(variants))
	for vi, v := range variants {
		m := Metrics{Name: v.Name}
		perSeed := make([]float64, seeds)
		perSeedN := make([]int, seeds)
		for _, sp := range splits {
			if nd, ok := score(&m, v.Rec(sp.train, sp.user, sp.hidden), sp.hidden, cfg.At); ok {
				perSeed[sp.seed] += nd
				perSeedN[sp.seed]++
			}
		}
		if m.Users > 0 {
			n := float64(m.Users)
			m.Precision /= n
			m.Recall /= n
			m.NDCG /= n
		}
		if m.predicted > 0 {
			m.MAE = m.absErr / m.predicted
			m.RMSE = math.Sqrt(m.sqErr / m.predicted)
		}
		if m.hidden > 0 {
			m.Coverage = m.predicted / m.hidden
		}
		if seeds > 1 {
			var xs []float64
			for s := range seeds {
				if perSeedN[s] > 0 {
					xs = append(xs, perSeed[s]/float64(perSeedN[s]))
				}
			}
			m.NDCGStd = stddev(xs)
		}
		out[vi] = m
	}
	return out
}

// score soma as métricas de um leitor e devolve o nDCG dele (ok = tinha
// algum livro relevante escondido).
func score(m *Metrics, recs []recommend.Recommendation, hidden []domain.Rating, at int) (float64, bool) {
	relevant := map[string]bool{}
	for _, h := range hidden {
		if h.Stars >= recommend.LikeThreshold {
			relevant[h.BookID] = true
		}
	}
	pred := make(map[string]float64, len(recs))
	for _, r := range recs {
		pred[r.BookID] = r.Predicted
	}
	for _, h := range hidden {
		m.hidden++
		if p, ok := pred[h.BookID]; ok {
			m.predicted++
			d := p - h.Stars
			m.absErr += math.Abs(d)
			m.sqErr += d * d
		}
	}
	if len(relevant) == 0 {
		return 0, false
	}
	hits := 0
	var dcg, idcg float64
	for i := 0; i < at && i < len(recs); i++ {
		if relevant[recs[i].BookID] {
			hits++
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	for i := 0; i < at && i < len(relevant); i++ {
		idcg += 1 / math.Log2(float64(i+2))
	}
	nd := dcg / idcg
	m.Users++
	m.Precision += float64(hits) / float64(at)
	m.Recall += float64(hits) / float64(len(relevant))
	m.NDCG += nd
	m.PerUser = append(m.PerUser, nd)
	return nd, true
}

// Delta é a diferença de nDCG de uma variante para a referência, com o
// intervalo de 95% do bootstrap pareado por leitor.
type Delta struct {
	Mean, Lo, Hi float64
}

// Significant informa se o intervalo não cruza o zero.
func (d Delta) Significant() bool { return d.Lo > 0 || d.Hi < 0 }

// Compare reamostra os leitores (com reposição) e mede a diferença média de
// nDCG entre m e ref, pareando o mesmo leitor nas duas. As duas precisam vir
// do mesmo Run.
func Compare(ref, m Metrics, iters int, seed int64) Delta {
	n := min(len(ref.PerUser), len(m.PerUser))
	if n == 0 {
		return Delta{}
	}
	diff := make([]float64, n)
	var mean float64
	for i := range n {
		diff[i] = m.PerUser[i] - ref.PerUser[i]
		mean += diff[i]
	}
	mean /= float64(n)
	rng := rand.New(rand.NewSource(seed))
	boot := make([]float64, iters)
	for it := range iters {
		var s float64
		for range n {
			s += diff[rng.Intn(n)]
		}
		boot[it] = s / float64(n)
	}
	sort.Float64s(boot)
	q := func(p float64) float64 { return boot[min(int(p*float64(iters)), iters-1)] }
	return Delta{Mean: mean, Lo: q(0.025), Hi: q(0.975)}
}

func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var mean float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return math.Sqrt(ss / float64(len(xs)-1))
}

// makeSplits escolhe os leitores de teste e esconde Holdout das notas de cada um.
func makeSplits(ratings map[string][]domain.Rating, cfg Config) []split {
	rng := rand.New(rand.NewSource(cfg.Seed))
	rated := func(u string) []domain.Rating {
		var out []domain.Rating
		for _, r := range ratings[u] {
			if r.Rated() {
				out = append(out, r)
			}
		}
		return out
	}
	var pool []string
	for u := range ratings {
		if u != cfg.Target && len(rated(u)) >= cfg.MinRatings {
			pool = append(pool, u)
		}
	}
	sort.Strings(pool)
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if cfg.Users >= 0 && len(pool) > cfg.Users {
		pool = pool[:cfg.Users]
	}
	if cfg.Target != "" && len(rated(cfg.Target)) >= cfg.MinRatings {
		pool = append([]string{cfg.Target}, pool...)
	}

	var out []split
	for _, u := range pool {
		rs := rated(u)
		if cfg.Temporal {
			sort.SliceStable(rs, func(i, j int) bool { return rs[i].ReadAt.Before(rs[j].ReadAt) })
		} else {
			rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
		}
		k := max(1, int(math.Round(cfg.Holdout*float64(len(rs)))))
		hidden := rs[len(rs)-k:]
		hide := map[string]bool{}
		for _, h := range hidden {
			hide[h.BookID] = true
		}
		var keep []domain.Rating
		for _, r := range ratings[u] {
			if !hide[r.BookID] {
				keep = append(keep, r)
			}
		}
		train := make(map[string][]domain.Rating, len(ratings))
		for k, v := range ratings {
			train[k] = v
		}
		if cut := hidden[0].ReadAt; cfg.Temporal && !cut.IsZero() {
			for k, v := range ratings {
				if k != u {
					train[k] = before(v, cut)
				}
			}
		}
		train[u] = keep
		out = append(out, split{user: u, train: train, hidden: hidden})
	}
	return out
}

// before devolve as leituras até a data de corte (as sem data ficam: não dá
// para saber, e tirá-las apagaria metade dos dados).
func before(rs []domain.Rating, cut time.Time) []domain.Rating {
	out := make([]domain.Rating, 0, len(rs))
	for _, r := range rs {
		if r.ReadAt.IsZero() || !r.ReadAt.After(cut) {
			out = append(out, r)
		}
	}
	return out
}
