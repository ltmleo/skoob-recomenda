package source

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// Memory é uma Source em memória; serve como cache do crawler e para testes.
type Memory struct {
	Ratings map[string][]domain.Rating // userID -> leituras
	Readers map[string][]string        // bookID -> userIDs
	Books   map[string]domain.Book
}

func NewMemory() *Memory {
	return &Memory{
		Ratings: map[string][]domain.Rating{},
		Readers: map[string][]string{},
		Books:   map[string]domain.Book{},
	}
}

// Add registra uma leitura mantendo o índice reverso livro -> leitores.
// Cada par (usuário, livro) aparece uma vez só: a mesma obra pode vir em
// duas edições na estante, e o mesmo leitor em páginas diferentes da API.
// No conflito fica a leitura mais informativa (ver better).
func (m *Memory) Add(r domain.Rating) {
	rs := m.Ratings[r.UserID]
	for i, cur := range rs {
		if cur.BookID == r.BookID {
			if better(r, cur) {
				rs[i] = r
			}
			return
		}
	}
	m.Ratings[r.UserID] = append(rs, r)
	m.Readers[r.BookID] = append(m.Readers[r.BookID], r.UserID)
}

// better decide qual de duas leituras do mesmo livro manter: com nota vence
// sem nota; depois, a mais recente.
func better(a, b domain.Rating) bool {
	if a.Rated() != b.Rated() {
		return a.Rated()
	}
	return a.ReadAt.After(b.ReadAt)
}

func (m *Memory) UserRatings(_ context.Context, userID string) ([]domain.Rating, error) {
	return m.Ratings[userID], nil
}

func (m *Memory) BookReaders(_ context.Context, bookID string, limit int) ([]string, error) {
	rs := m.Readers[bookID]
	if limit > 0 && len(rs) > limit {
		rs = rs[:limit]
	}
	return rs, nil
}

func (m *Memory) Book(_ context.Context, bookID string) (domain.Book, error) {
	if b, ok := m.Books[bookID]; ok {
		return b, nil
	}
	return domain.Book{ID: bookID, Title: bookID}, nil
}

// LoadCSV lê um CSV com cabeçalho:
// user_id,book_id,title,author,rating,read_at   (read_at em YYYY-MM-DD, opcional)
func LoadCSV(path string) (*Memory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadCSV(f)
}

func ReadCSV(r io.Reader) (*Memory, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("cabeçalho: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, req := range []string{"user_id", "book_id", "rating"} {
		if _, ok := col[req]; !ok {
			return nil, fmt.Errorf("coluna obrigatória ausente: %s", req)
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	m := NewMemory()
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("linha %d: %w", line, err)
		}
		stars, err := strconv.ParseFloat(strings.ReplaceAll(get(rec, "rating"), ",", "."), 64)
		if err != nil || stars < 0 || stars > 5 {
			return nil, fmt.Errorf("linha %d: nota inválida %q", line, get(rec, "rating"))
		}
		var readAt time.Time
		if s := get(rec, "read_at"); s != "" {
			if readAt, err = time.Parse("2006-01-02", s); err != nil {
				return nil, fmt.Errorf("linha %d: data inválida %q", line, s)
			}
		}
		bookID := get(rec, "book_id")
		m.MergeBook(domain.Book{ID: bookID, Title: get(rec, "title"), Author: get(rec, "author"),
			EditionID: get(rec, "edition_id"), Cover: get(rec, "cover")})
		m.Add(domain.Rating{UserID: get(rec, "user_id"), BookID: bookID, Stars: stars, ReadAt: readAt,
			Status: get(rec, "status")})
	}
	return m, nil
}

// ReplaceUser troca a estante inteira de um usuário (mantém o índice reverso).
func (m *Memory) ReplaceUser(userID string, rs []domain.Rating) {
	for _, old := range m.Ratings[userID] {
		readers := m.Readers[old.BookID]
		kept := readers[:0]
		for _, u := range readers {
			if u != userID {
				kept = append(kept, u)
			}
		}
		if len(kept) == 0 {
			delete(m.Readers, old.BookID)
		} else {
			m.Readers[old.BookID] = kept
		}
	}
	delete(m.Ratings, userID)
	for _, r := range rs {
		m.Add(r)
	}
}

// MergeBook registra metadados, preenchendo só os campos ainda vazios.
func (m *Memory) MergeBook(b domain.Book) {
	cur := m.Books[b.ID]
	cur.ID = b.ID
	if cur.Title == "" || cur.Title == b.ID {
		cur.Title = b.Title
	}
	if cur.Title == "" {
		cur.Title = b.ID
	}
	if cur.Author == "" {
		cur.Author = b.Author
	}
	if cur.EditionID == "" {
		cur.EditionID = b.EditionID
	}
	if cur.Cover == "" {
		cur.Cover = b.Cover
	}
	m.Books[b.ID] = cur
}

// WriteCSV persiste a Memory no mesmo formato de LoadCSV (cache do crawler).
// A ordem é estável (usuário, depois a ordem da estante), para que regravar
// os mesmos dados gere o mesmo arquivo.
func (m *Memory) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"user_id", "book_id", "title", "author", "rating", "read_at", "status", "edition_id", "cover"})
	users := make([]string, 0, len(m.Ratings))
	for u := range m.Ratings {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		for _, r := range m.Ratings[u] {
			b := m.Books[r.BookID]
			d := ""
			if !r.ReadAt.IsZero() {
				d = r.ReadAt.Format("2006-01-02")
			}
			if err := cw.Write([]string{r.UserID, r.BookID, b.Title, b.Author,
				strconv.FormatFloat(r.Stars, 'f', -1, 64), d, r.Status, b.EditionID, b.Cover}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}
