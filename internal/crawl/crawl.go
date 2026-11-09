// Package crawl monta a vizinhança do usuário-alvo como um grafo bipartido
// usuário <-> livro, por busca em largura limitada (BFS).
//
//	alvo ──leu──> livro (nota alta) ──lido por──> vizinho ──leu──> candidatos
//
// Só a vizinhança relevante é coletada: o custo é
// O(livros_semente × leitores_por_livro), não o site inteiro.
package crawl

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

type Options struct {
	SeedBooks      int     // quantos livros do alvo usar como sementes (os mais bem avaliados/recentes)
	ReadersPerBook int     // leitores coletados por livro semente
	MaxUsers       int     // teto total de estantes baixadas
	MinSeedStars   float64 // só expande a partir de livros com nota >= isso
	Progress       func(done, total int)
}

// DefaultOptions são os padrões da CLI (`recomenda skoob`). O intervalo
// entre requisições fica na fonte (source.Skoob.Interval), não aqui.
func DefaultOptions() Options {
	return Options{SeedBooks: 20, ReadersPerBook: 15, MaxUsers: 150, MinSeedStars: 4}
}

// fatal informa se um erro deve interromper a coleta inteira (cancelamento
// ou sessão inválida), em vez de só pular aquele item.
func fatal(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, source.ErrUnauthorized)
}

// Collect baixa a estante do alvo e dos vizinhos e devolve tudo em memória.
// Se for interrompida (Ctrl+C, sessão expirada), devolve o que já veio,
// sempre com os metadados dos livros, junto com o erro.
func Collect(ctx context.Context, src source.Source, target string, o Options) (mem *source.Memory, err error) {
	mem = source.NewMemory()
	// Os metadados (título, edição, capa) entram mesmo numa saída antecipada:
	// sem a edição, o livro não pode ser expandido depois no `serve`.
	defer func() {
		if mem == nil {
			return
		}
		for id := range mem.Readers {
			if b, berr := src.Book(context.WithoutCancel(ctx), id); berr == nil {
				mem.MergeBook(b)
			}
		}
	}()

	mine, err := src.UserRatings(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("estante do alvo: %w", err)
	}
	if len(mine) == 0 {
		return nil, fmt.Errorf("usuário %s sem leituras (estante privada?)", target)
	}
	for _, r := range mine {
		mem.Add(r)
	}

	// Nível 1: livros semente — nota alta, mais recentes primeiro.
	seeds := make([]domain.Rating, 0, len(mem.Ratings[target]))
	for _, r := range mem.Ratings[target] {
		if r.Stars >= o.MinSeedStars {
			seeds = append(seeds, r)
		}
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		if seeds[i].Stars != seeds[j].Stars {
			return seeds[i].Stars > seeds[j].Stars
		}
		return seeds[i].ReadAt.After(seeds[j].ReadAt)
	})
	if len(seeds) > o.SeedBooks {
		seeds = seeds[:o.SeedBooks]
	}

	// Nível 2: leitores desses livros (deduplicados). Uma semente que falha
	// (ex.: edição desconhecida) é pulada; as outras seguem.
	seen := map[string]bool{target: true}
	var neighbors []string
	var seedErrs []error
	for _, s := range seeds {
		if len(neighbors) >= o.MaxUsers {
			break
		}
		readers, rerr := src.BookReaders(ctx, s.BookID, o.ReadersPerBook)
		if rerr != nil {
			if fatal(ctx, rerr) {
				return mem, fmt.Errorf("leitores de %s: %w", s.BookID, rerr)
			}
			seedErrs = append(seedErrs, fmt.Errorf("leitores de %s: %w", s.BookID, rerr))
		}
		for _, u := range readers {
			if !seen[u] && len(neighbors) < o.MaxUsers {
				seen[u] = true
				neighbors = append(neighbors, u)
			}
		}
	}
	if len(neighbors) == 0 && len(seedErrs) > 0 {
		return mem, fmt.Errorf("nenhuma semente funcionou: %w", errors.Join(seedErrs...))
	}

	// Nível 3: estantes dos vizinhos. Falha individual não aborta a coleta.
	for i, u := range neighbors {
		rs, uerr := src.UserRatings(ctx, u)
		if uerr != nil && fatal(ctx, uerr) {
			return mem, uerr
		}
		for _, r := range rs {
			mem.Add(r)
		}
		if o.Progress != nil {
			o.Progress(i+1, len(neighbors))
		}
	}
	return mem, nil
}

// ExpandBook segue o grafo a partir de um livro: busca até readers pessoas
// que deram nota a ele e baixa as estantes das que ainda não estão em mem.
// Devolve quantas estantes novas entraram. Falha individual não aborta.
func ExpandBook(ctx context.Context, src source.Source, mem *source.Memory, bookID string, readers int,
	progress func(done, total int)) (int, error) {
	users, err := src.BookReaders(ctx, bookID, readers)
	if err != nil {
		return 0, fmt.Errorf("leitores de %s: %w", bookID, err)
	}
	var todo []string
	queued := map[string]bool{}
	for _, u := range users {
		if _, ok := mem.Ratings[u]; !ok && !queued[u] {
			queued[u] = true
			todo = append(todo, u)
		}
	}
	added := 0
	for i, u := range todo {
		if ctx.Err() != nil {
			return added, ctx.Err()
		}
		rs, err := src.UserRatings(ctx, u)
		if err != nil && fatal(ctx, err) {
			return added, err
		}
		if err == nil && len(rs) > 0 {
			for _, r := range rs {
				mem.Add(r)
				if b, err := src.Book(ctx, r.BookID); err == nil {
					mem.MergeBook(b)
				}
			}
			added++
		}
		if progress != nil {
			progress(i+1, len(todo))
		}
	}
	return added, nil
}
