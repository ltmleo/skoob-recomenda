package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// fakeSkoob imita o formato observado em prd-api.skoob.com.br.
func fakeSkoob(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "jwt" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"token invalid"}`)
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/v1/bookshelf" && q.Get("user_id") == "me" && q.Get("page") == "1":
			fmt.Fprint(w, `{"total_pages":2,"total_items":3,"items":[
				{"rating":4.5,"status":"read","book_id":10,"edition_id":110,"title":"A","author":"X","finished_at":"2026-10-18T03:00:00.000Z"},
				{"rating":0,"status":"want_to_read","book_id":11,"edition_id":111,"title":"B","author":"Y","finished_at":null}]}`)
		case r.URL.Path == "/v1/bookshelf" && q.Get("user_id") == "me" && q.Get("page") == "2":
			fmt.Fprint(w, `{"total_pages":2,"total_items":3,"items":[
				{"rating":0,"status":"read","book_id":12,"edition_id":112,"title":"C","author":"Z","finished_at":null}]}`)
		case r.URL.Path == "/v1/book/110/users-rated":
			fmt.Fprint(w, `{"total_pages":1,"items":[{"id":"u1","rating":5},{"id":"me","is_my_profile":true},{"id":"u2"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSkoobSource(t *testing.T) {
	srv := fakeSkoob(t)
	defer srv.Close()
	ctx := context.Background()
	s := NewSkoob(srv.URL, "jwt")
	s.Interval = 0

	rs, err := s.UserRatings(ctx, "me")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 || rs[0].BookID != "10" || rs[0].Stars != 4.5 || rs[1].Status != "want_to_read" || rs[2].BookID != "12" {
		t.Fatalf("estante: %+v (esperava os 3 itens com status)", rs)
	}
	if rs[0].ReadAt.Format("2006-01-02") != "2026-10-18" || !rs[2].ReadAt.IsZero() {
		t.Fatalf("datas: %v %v", rs[0].ReadAt, rs[2].ReadAt)
	}
	if rs[1].Consumed() || !rs[2].Consumed() {
		t.Fatal("quero ler não conta como lido; lido sem nota conta")
	}
	if b, _ := s.Book(ctx, "10"); b.Title != "A" || b.Author != "X" {
		t.Fatalf("metadados: %+v", b)
	}

	readers, err := s.BookReaders(ctx, "10", 10)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(readers) != "[u1 u2]" {
		t.Fatalf("leitores: %v (sem o próprio usuário)", readers)
	}
	if one, _ := s.BookReaders(ctx, "10", 1); len(one) != 1 {
		t.Fatalf("limit não respeitado: %v", one)
	}
	if _, err := s.BookReaders(ctx, "999", 10); err == nil {
		t.Fatal("livro sem edição conhecida deveria falhar")
	}
	fresh := NewSkoob(srv.URL, "jwt")
	fresh.Interval = 0
	fresh.Seed(map[string]domain.Book{"10": {ID: "10", EditionID: "110"}})
	if got, err := fresh.BookReaders(ctx, "10", 10); err != nil || len(got) != 2 {
		t.Fatalf("Seed deveria ensinar a edição: %v %v", got, err)
	}

	bad := NewSkoob(srv.URL, "velho")
	bad.Interval = 0
	if _, err := bad.UserRatings(ctx, "me"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("esperava ErrUnauthorized, got %v", err)
	}
}
