package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// StatsCache guarda a avaliação geral de cada livro (bookID -> BookStats).
// A média de um livro muda devagar, então o cache vale por semanas e evita
// uma requisição por livro a cada execução. Seguro para uso concorrente.
type StatsCache struct {
	mu    sync.RWMutex
	books map[string]domain.BookStats
}

func NewStatsCache() *StatsCache { return &StatsCache{books: map[string]domain.BookStats{}} }

// StatsPath é o cache que acompanha um CSV de dados (ex.: skoob.csv.livros.json).
func StatsPath(dataPath string) string { return dataPath + ".livros.json" }

// LoadStats lê o cache; arquivo inexistente devolve um cache vazio.
func LoadStats(path string) (*StatsCache, error) {
	c := NewStatsCache()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c.books); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.books == nil {
		c.books = map[string]domain.BookStats{}
	}
	return c, nil
}

// Save grava o cache de forma atômica (arquivo temporário + rename).
func (c *StatsCache) Save(path string) error {
	c.mu.RLock()
	b, err := json.Marshal(c.books)
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (c *StatsCache) Get(bookID string) (domain.BookStats, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.books[bookID]
	return s, ok
}

func (c *StatsCache) Set(bookID string, s domain.BookStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.books[bookID] = s
}

func (c *StatsCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.books)
}

// Fresh informa se o livro está no cache e foi buscado há menos de maxAge
// (maxAge <= 0: qualquer idade serve).
func (c *StatsCache) Fresh(bookID string, maxAge time.Duration, now time.Time) bool {
	s, ok := c.Get(bookID)
	return ok && (maxAge <= 0 || now.Sub(s.FetchedAt) < maxAge)
}

type bookPage struct {
	Ratings struct {
		Average float64 `json:"average_rating"`
		Count   int     `json:"count_ratings"`
	} `json:"ratings"`
}

// BookStats busca a avaliação geral do livro: GET /v1/book/EDICAO.
// Não há endpoint em lote; é uma requisição por livro, com o mesmo throttle.
func (s *Skoob) BookStats(ctx context.Context, bookID string) (domain.BookStats, error) {
	s.mu.Lock()
	ed := s.books[bookID].EditionID
	s.mu.Unlock()
	if ed == "" {
		return domain.BookStats{}, fmt.Errorf("edição desconhecida para o livro %s", bookID)
	}
	var p bookPage
	if err := s.get(ctx, "/v1/book/"+url.PathEscape(ed), url.Values{}, &p); err != nil {
		return domain.BookStats{}, err
	}
	return domain.BookStats{Avg: p.Ratings.Average, Count: p.Ratings.Count, FetchedAt: time.Now()}, nil
}
