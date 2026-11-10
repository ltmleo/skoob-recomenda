package crawl

import (
	"context"
	"sort"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

// StatsFetcher busca a avaliação geral de um livro (source.Skoob implementa).
type StatsFetcher interface {
	BookStats(ctx context.Context, bookID string) (domain.BookStats, error)
}

// Eligible lista os livros que podem virar recomendação: os que têm nota de
// pelo menos minRaters leitores na coleta (o algoritmo exige MinSupport
// vizinhos ou leitores em comum, então os demais nunca aparecem). Os mais
// avaliados vêm primeiro, para que uma coleta interrompida cubra o que importa.
func Eligible(ratings map[string][]domain.Rating, minRaters int) []string {
	n := map[string]int{}
	for _, rs := range ratings {
		for _, r := range rs {
			if r.Rated() {
				n[r.BookID]++
			}
		}
	}
	var out []string
	for b, c := range n {
		if c >= minRaters {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if n[out[i]] != n[out[j]] {
			return n[out[i]] > n[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// FillStats busca no Skoob a avaliação geral dos livros que faltam no cache
// (ou estão mais velhos que maxAge) e devolve quantos buscou. Falha de um
// livro não aborta; cancelamento e sessão inválida, sim (o que já veio fica
// no cache). save, se não for nil, é chamado a cada saveEvery livros.
func FillStats(ctx context.Context, src StatsFetcher, cache *source.StatsCache, ids []string, maxAge time.Duration,
	save func() error, progress func(done, total int)) (int, error) {
	now := time.Now()
	var todo []string
	for _, id := range ids {
		if !cache.Fresh(id, maxAge, now) {
			todo = append(todo, id)
		}
	}
	const saveEvery = 50
	fetched := 0
	for i, id := range todo {
		if ctx.Err() != nil {
			return fetched, ctx.Err()
		}
		st, err := src.BookStats(ctx, id)
		if err != nil && fatal(ctx, err) {
			return fetched, err
		}
		if err == nil {
			cache.Set(id, st)
			fetched++
			if save != nil && fetched%saveEvery == 0 {
				if err := save(); err != nil {
					return fetched, err
				}
			}
		}
		if progress != nil {
			progress(i+1, len(todo))
		}
	}
	return fetched, nil
}
