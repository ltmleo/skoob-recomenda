package crawl

import (
	"context"
	"errors"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

// flaky embrulha uma Memory: falha os leitores de alguns livros, conta
// requisições e pode cancelar o contexto no meio da coleta.
type flaky struct {
	*source.Memory
	badBooks map[string]error
	cancelAt int // cancela na N-ésima estante de vizinho (0 = nunca)
	cancel   context.CancelFunc
	shelves  int
}

func (f *flaky) BookReaders(ctx context.Context, id string, n int) ([]string, error) {
	if err := f.badBooks[id]; err != nil {
		return nil, err
	}
	return f.Memory.BookReaders(ctx, id, n)
}

func (f *flaky) UserRatings(ctx context.Context, u string) ([]domain.Rating, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if u != "me" {
		f.shelves++
		if f.shelves == f.cancelAt {
			f.cancel()
			return nil, context.Canceled
		}
	}
	return f.Memory.UserRatings(ctx, u)
}

func graph() *source.Memory {
	m := source.NewMemory()
	for _, r := range []domain.Rating{
		{UserID: "me", BookID: "sem-edicao", Stars: 5},
		{UserID: "me", BookID: "duna", Stars: 5},
		{UserID: "fan1", BookID: "duna", Stars: 5},
		{UserID: "fan1", BookID: "fundacao", Stars: 5},
		{UserID: "fan2", BookID: "duna", Stars: 4},
		{UserID: "fan2", BookID: "fundacao", Stars: 4},
	} {
		m.Add(r)
	}
	m.MergeBook(domain.Book{ID: "duna", Title: "Duna", EditionID: "9"})
	m.MergeBook(domain.Book{ID: "fundacao", Title: "Fundação", EditionID: "8"})
	return m
}

// Uma semente com erro (ex.: edição desconhecida) não pode zerar a coleta.
func TestCollectSkipsFailingSeed(t *testing.T) {
	src := &flaky{Memory: graph(), badBooks: map[string]error{"sem-edicao": errors.New("edição desconhecida")}}
	mem, err := Collect(context.Background(), src, "me", DefaultOptions())
	if err != nil {
		t.Fatalf("semente ruim não deveria abortar: %v", err)
	}
	if len(mem.Ratings["fan1"]) == 0 || len(mem.Ratings["fan2"]) == 0 {
		t.Fatalf("deveria ter coletado os leitores de duna: %v", mem.Ratings)
	}
}

func TestCollectFailsWhenEverySeedFails(t *testing.T) {
	bad := errors.New("fora do ar")
	src := &flaky{Memory: graph(), badBooks: map[string]error{"sem-edicao": bad, "duna": bad}}
	if _, err := Collect(context.Background(), src, "me", DefaultOptions()); !errors.Is(err, bad) {
		t.Fatalf("esperava o erro das sementes, veio %v", err)
	}
}

// Ctrl+C no meio: devolve o parcial COM os metadados (título, edição).
func TestCollectKeepsMetadataWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &flaky{Memory: graph(), cancelAt: 2, cancel: cancel}
	mem, err := Collect(ctx, src, "me", DefaultOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("esperava cancelamento, veio %v", err)
	}
	if len(mem.Ratings["fan1"]) == 0 {
		t.Fatal("a primeira estante já baixada deveria vir no parcial")
	}
	if b := mem.Books["duna"]; b.Title != "Duna" || b.EditionID != "9" {
		t.Fatalf("metadados perdidos no cancelamento: %+v", b)
	}
	if b := mem.Books["fundacao"]; b.EditionID != "8" {
		t.Fatalf("metadados dos livros dos vizinhos também: %+v", b)
	}
}

func TestCollectAbortsOnUnauthorized(t *testing.T) {
	src := &flaky{Memory: graph(), badBooks: map[string]error{"sem-edicao": source.ErrUnauthorized}}
	if _, err := Collect(context.Background(), src, "me", DefaultOptions()); !errors.Is(err, source.ErrUnauthorized) {
		t.Fatalf("sessão inválida deve parar tudo, veio %v", err)
	}
}
