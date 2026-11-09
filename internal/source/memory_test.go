package source

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

func TestAddDeduplicatesUserBook(t *testing.T) {
	m := NewMemory()
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.Add(domain.Rating{UserID: "u", BookID: "b", Status: "read"})              // edição 1, sem nota
	m.Add(domain.Rating{UserID: "u", BookID: "b", Stars: 4, ReadAt: d})         // edição 2, com nota
	m.Add(domain.Rating{UserID: "u", BookID: "b", Stars: 2, ReadAt: d.Add(-1)}) // mais antiga: perde
	m.Add(domain.Rating{UserID: "u", BookID: "b"})                              // sem nota: perde
	if len(m.Ratings["u"]) != 1 || m.Ratings["u"][0].Stars != 4 {
		t.Fatalf("esperava uma leitura com nota 4: %+v", m.Ratings["u"])
	}
	if len(m.Readers["b"]) != 1 {
		t.Fatalf("índice reverso duplicado: %v", m.Readers["b"])
	}
}

func TestWriteCSVIsDeterministic(t *testing.T) {
	m := NewMemory()
	for _, u := range []string{"z", "a", "m", "c", "q"} {
		m.Add(domain.Rating{UserID: u, BookID: "b", Stars: 3})
	}
	var first bytes.Buffer
	_ = m.WriteCSV(&first)
	for range 5 {
		var again bytes.Buffer
		_ = m.WriteCSV(&again)
		if again.String() != first.String() {
			t.Fatal("WriteCSV mudou a ordem entre gravações")
		}
	}
}

func TestRetryAfter(t *testing.T) {
	fb := 7 * time.Second
	cases := map[string]time.Duration{"": fb, "lixo": fb, "3": 3 * time.Second, "-1": fb, "9999": 2 * time.Minute}
	for h, want := range cases {
		if got := retryAfter(h, fb); got != want {
			t.Errorf("Retry-After %q: got %v, want %v", h, got, want)
		}
	}
	if got := retryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), fb); got != 0 {
		t.Errorf("data no passado: got %v", got)
	}
}

func TestOwnerShelfIsNeverTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("page")
		fmt.Fprintf(w, `{"total_pages":3,"items":[{"rating":4,"status":"read","book_id":%s,"edition_id":1}]}`, p)
	}))
	defer srv.Close()
	s := NewSkoob(srv.URL, "jwt")
	s.Interval, s.MaxPages, s.Owner = 0, 1, "me"
	other, _ := s.UserRatings(context.Background(), "outro")
	mine, _ := s.UserRatings(context.Background(), "me")
	if len(other) != 1 || len(mine) != 3 {
		t.Fatalf("terceiros param em MaxPages (1), o dono vem inteiro (3): %d, %d", len(other), len(mine))
	}
}
