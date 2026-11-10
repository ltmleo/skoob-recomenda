package crawl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

type fakeStats struct {
	calls []string
	fail  map[string]error
}

func (f *fakeStats) BookStats(_ context.Context, id string) (domain.BookStats, error) {
	f.calls = append(f.calls, id)
	if err := f.fail[id]; err != nil {
		return domain.BookStats{}, err
	}
	return domain.BookStats{Avg: 4, Count: 10, FetchedAt: time.Now()}, nil
}

func TestEligibleOrdersByRaters(t *testing.T) {
	rs := map[string][]domain.Rating{
		"a": {{BookID: "pop", Stars: 5}, {BookID: "mid", Stars: 4}, {BookID: "solo", Stars: 3}, {BookID: "unrated"}},
		"b": {{BookID: "pop", Stars: 4}, {BookID: "mid", Stars: 2}, {BookID: "unrated"}},
		"c": {{BookID: "pop", Stars: 1}},
	}
	got := Eligible(rs, 2)
	if len(got) != 2 || got[0] != "pop" || got[1] != "mid" {
		t.Fatalf("só livros com 2+ notas, mais avaliados primeiro: %v", got)
	}
}

func TestFillStatsSkipsFreshAndSurvivesErrors(t *testing.T) {
	cache := source.NewStatsCache()
	cache.Set("fresh", domain.BookStats{Avg: 3, Count: 1, FetchedAt: time.Now()})
	cache.Set("old", domain.BookStats{Avg: 3, Count: 1, FetchedAt: time.Now().Add(-90 * 24 * time.Hour)})
	f := &fakeStats{fail: map[string]error{"bad": errors.New("404")}}
	n, err := FillStats(context.Background(), f, cache, []string{"fresh", "old", "bad", "new"}, 30*24*time.Hour, nil, nil)
	if err != nil || n != 2 {
		t.Fatalf("deveria buscar old e new (bad falha sem abortar): n=%d err=%v calls=%v", n, err, f.calls)
	}
	if len(f.calls) != 3 {
		t.Fatalf("fresh não deveria ser buscado: %v", f.calls)
	}
	if st, _ := cache.Get("old"); st.Avg != 4 {
		t.Fatal("old deveria ser atualizado")
	}
}

func TestFillStatsStopsOnUnauthorized(t *testing.T) {
	f := &fakeStats{fail: map[string]error{"a": source.ErrUnauthorized}}
	_, err := FillStats(context.Background(), f, source.NewStatsCache(), []string{"a", "b"}, 0, nil, nil)
	if !errors.Is(err, source.ErrUnauthorized) || len(f.calls) != 1 {
		t.Fatalf("sessão inválida deveria parar: %v %v", err, f.calls)
	}
}
