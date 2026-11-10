package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

func TestStatsCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.csv.livros.json")
	c, err := LoadStats(path)
	if err != nil || c.Len() != 0 {
		t.Fatalf("arquivo inexistente deveria dar cache vazio: %v %d", err, c.Len())
	}
	now := time.Now()
	c.Set("6505", domain.BookStats{Avg: 3.7, Count: 980, FetchedAt: now.Add(-48 * time.Hour)})
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	d, err := LoadStats(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := d.Get("6505"); !ok || st.Avg != 3.7 || st.Count != 980 {
		t.Fatalf("releitura: %+v", st)
	}
	if !d.Fresh("6505", 72*time.Hour, now) || d.Fresh("6505", 24*time.Hour, now) || d.Fresh("x", 0, now) {
		t.Fatal("Fresh deveria respeitar maxAge e livros ausentes")
	}
}

func TestSkoobBookStats(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"book_id":6505,"ratings":{"average_rating":3.7,"count_ratings":980}}`))
	}))
	defer srv.Close()
	s := NewSkoob(srv.URL, "tok")
	s.Interval = 0
	if _, err := s.BookStats(context.Background(), "6505"); err == nil {
		t.Fatal("sem edição conhecida deveria falhar")
	}
	s.Seed(map[string]domain.Book{"6505": {ID: "6505", EditionID: "390413"}})
	st, err := s.BookStats(context.Background(), "6505")
	if err != nil || st.Avg != 3.7 || st.Count != 980 || st.FetchedAt.IsZero() {
		t.Fatalf("%+v %v", st, err)
	}
	if path != "/v1/book/390413" {
		t.Fatalf("deveria consultar pela edição: %s", path)
	}
}
