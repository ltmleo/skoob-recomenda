// Package eval mede a qualidade das recomendações offline: esconde parte das
// notas de alguns leitores, recomenda com o resto e confere se os livros
// escondidos de que eles gostaram aparecem no topo.
//
//	Precision@K = acertos no top-K / K
//	Recall@K    = acertos no top-K / livros escondidos com nota >= 4
//	nDCG@K      = ganho descontado pela posição (acertar no 1º vale mais que no 20º)
//	MAE / RMSE  = erro da nota prevista nos livros escondidos que receberam previsão
//	Cobertura   = fração dos escondidos para os quais o algoritmo previu algo
package eval

import (
	"math"
	"math/rand"
	"sort"

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
	Temporal   bool    // esconde as leituras mais recentes em vez de sorteá-las
	Seed       int64
}

func DefaultConfig() Config {
	return Config{Users: 40, MinRatings: 20, Holdout: 0.2, At: 20, Seed: 1}
}

// Variant é um algoritmo a comparar. Rec devolve a lista completa ordenada.
type Variant struct {
	Name string
	Rec  func(ratings map[string][]domain.Rating, user string) []recommend.Recommendation
}

// KNN embrulha recommend.Recommend com as opções dadas.
func KNN(name string, o recommend.Options) Variant {
	o.MaxPerAuthor = 0 // diversidade é apresentação; não entra na métrica
	return Variant{Name: name, Rec: func(rs map[string][]domain.Rating, u string) []recommend.Recommendation {
		return recommend.Recommend(rs, u, 0, o).Recommendations
	}}
}

// PopularVariant é o baseline de popularidade.
func PopularVariant() Variant {
	return Variant{Name: "popularidade", Rec: func(rs map[string][]domain.Rating, u string) []recommend.Recommendation {
		return recommend.Popular(rs, u, 0)
	}}
}

// Metrics resume uma variante (médias por leitor testado).
type Metrics struct {
	Name                     string
	Users                    int // leitores com algum livro relevante escondido
	Precision, Recall, NDCG  float64
	MAE, RMSE, Coverage      float64
	predicted, hidden, sqErr float64
	absErr                   float64
}

type split struct {
	user   string
	train  map[string][]domain.Rating
	hidden []domain.Rating
}

// Run avalia todas as variantes sobre os mesmos cortes (mesma semente).
func Run(ratings map[string][]domain.Rating, cfg Config, variants ...Variant) []Metrics {
	splits := makeSplits(ratings, cfg)
	out := make([]Metrics, len(variants))
	for vi, v := range variants {
		m := Metrics{Name: v.Name}
		for _, sp := range splits {
			score(&m, v.Rec(sp.train, sp.user), sp.hidden, cfg.At)
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
		out[vi] = m
	}
	return out
}

func score(m *Metrics, recs []recommend.Recommendation, hidden []domain.Rating, at int) {
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
		return
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
	m.Users++
	m.Precision += float64(hits) / float64(at)
	m.Recall += float64(hits) / float64(len(relevant))
	m.NDCG += dcg / idcg
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
		train[u] = keep
		out = append(out, split{user: u, train: train, hidden: hidden})
	}
	return out
}
