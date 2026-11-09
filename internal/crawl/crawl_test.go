package crawl

import (
	"context"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

func TestCollectExpandsOnlyFromHighRatedSeeds(t *testing.T) {
	src := source.NewMemory()
	for _, x := range []domain.Rating{
		{UserID: "me", BookID: "loved", Stars: 5},
		{UserID: "me", BookID: "hated", Stars: 1},
		{UserID: "fan", BookID: "loved", Stars: 5},
		{UserID: "fan", BookID: "other", Stars: 4},
		{UserID: "hater", BookID: "hated", Stars: 1},
		{UserID: "stranger", BookID: "other", Stars: 3},
	} {
		src.Add(x)
	}
	o := DefaultOptions()
	mem, err := Collect(context.Background(), src, "me", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mem.Ratings["fan"]; !ok {
		t.Error("deveria coletar leitor de livro bem avaliado")
	}
	if _, ok := mem.Ratings["hater"]; ok {
		t.Error("não deveria expandir a partir de livro mal avaliado")
	}
	if _, ok := mem.Ratings["stranger"]; ok {
		t.Error("BFS deveria parar no nível dos vizinhos (sem 3º salto)")
	}
}

func TestCollectFailsOnEmptyShelf(t *testing.T) {
	if _, err := Collect(context.Background(), source.NewMemory(), "ghost", DefaultOptions()); err == nil {
		t.Fatal("esperava erro para estante vazia")
	}
}

func TestExpandBookAddsOnlyNewReaders(t *testing.T) {
	src := source.NewMemory()
	for _, x := range []domain.Rating{
		{UserID: "me", BookID: "duna", Stars: 5},
		{UserID: "known", BookID: "duna", Stars: 4},
		{UserID: "new", BookID: "duna", Stars: 5},
		{UserID: "new", BookID: "fundacao", Stars: 5},
	} {
		src.Add(x)
	}
	mem := source.NewMemory()
	mem.Add(domain.Rating{UserID: "me", BookID: "duna", Stars: 5})
	mem.Add(domain.Rating{UserID: "known", BookID: "duna", Stars: 4})
	n, err := ExpandBook(context.Background(), src, mem, "duna", 10, nil)
	if err != nil || n != 1 {
		t.Fatalf("esperava 1 estante nova, got %d (%v)", n, err)
	}
	if len(mem.Ratings["new"]) != 2 || len(mem.Ratings["known"]) != 1 {
		t.Fatalf("estantes: %+v", mem.Ratings)
	}
}
