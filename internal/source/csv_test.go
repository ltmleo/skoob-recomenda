package source

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

func TestCSVRoundTrip(t *testing.T) {
	in := "user_id,book_id,title,author,rating,read_at\nu1,b1,Duna,Frank Herbert,\"4,5\",2026-01-02\nu2,b1,Duna,Frank Herbert,5,\n"
	m, err := ReadCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Ratings["u1"][0].Stars; got != 4.5 {
		t.Fatalf("nota com vírgula: %v", got)
	}
	if len(m.Readers["b1"]) != 2 || m.Books["b1"].Author != "Frank Herbert" {
		t.Fatalf("índice/metadados: %+v %+v", m.Readers, m.Books)
	}
	var buf bytes.Buffer
	if err := m.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	m2, err := ReadCSV(&buf)
	if err != nil || len(m2.Ratings["u1"]) != 1 || !m2.Ratings["u1"][0].ReadAt.Equal(m.Ratings["u1"][0].ReadAt) {
		t.Fatalf("round trip: %v %+v", err, m2.Ratings)
	}
}

func TestCSVKeepsStatusEditionAndCover(t *testing.T) {
	in := "user_id,book_id,title,author,rating,read_at,status,edition_id,cover\n" +
		"me,b1,Duna,FH,0,,want_to_read,,\n" +
		"u2,b1,Duna,FH,5,,read,101,http://c/1.jpg\n"
	m, err := ReadCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if m.Ratings["me"][0].Status != "want_to_read" {
		t.Fatalf("status: %+v", m.Ratings["me"][0])
	}
	if b := m.Books["b1"]; b.EditionID != "101" || b.Cover != "http://c/1.jpg" {
		t.Fatalf("metadados devem ser completados por linhas posteriores: %+v", b)
	}
	var buf bytes.Buffer
	_ = m.WriteCSV(&buf)
	m2, _ := ReadCSV(&buf)
	if m2.Ratings["me"][0].Status != "want_to_read" || m2.Books["b1"].EditionID != "101" {
		t.Fatalf("round trip perdeu campos: %+v %+v", m2.Ratings["me"], m2.Books["b1"])
	}
}

func TestCSVRejectsBadRating(t *testing.T) {
	if _, err := ReadCSV(strings.NewReader("user_id,book_id,rating\nu,b,7\n")); err == nil {
		t.Fatal("esperava erro para nota 7")
	}
}

func TestReplaceUserKeepsReverseIndex(t *testing.T) {
	m := NewMemory()
	m.Add(domain.Rating{UserID: "me", BookID: "a", Stars: 5})
	m.Add(domain.Rating{UserID: "u", BookID: "a", Stars: 4})
	m.Add(domain.Rating{UserID: "me", BookID: "b", Stars: 3})
	m.ReplaceUser("me", []domain.Rating{{UserID: "me", BookID: "c", Stars: 5, Status: "read"}})
	if len(m.Ratings["me"]) != 1 || len(m.Readers["a"]) != 1 || m.Readers["b"] != nil || len(m.Readers["c"]) != 1 {
		t.Fatalf("ratings=%v readers=%v", m.Ratings, m.Readers)
	}
}
