package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// ErrUnauthorized indica token ausente, inválido ou expirado.
var ErrUnauthorized = errors.New("sessão inválida ou expirada: rode `recomenda login`")

// Skoob implementa Source sobre a API (não documentada) usada pelo site:
//
//	GET /v1/bookshelf?user_id=ID&page=N&limit=L   estante (nota, status, data)
//	GET /v1/book/EDICAO/users-rated?page=N&limit=L quem avaliou uma edição
//
// IDs de usuário são os hex do site novo (ex.: 67bd03f4...). O livro é
// identificado pelo book_id (obra), não pela edição; a edição vista em alguma
// estante é guardada para consultar os leitores.
type Skoob struct {
	Base     string        // ex.: auth.APIBase
	Token    string        // JWT (auth.UnwrapToken)
	Interval time.Duration // intervalo mínimo entre requisições
	PageSize int           // itens por página
	MaxPages int           // teto de páginas por estante de terceiros (0 = sem teto)
	Owner    string        // ID do usuário logado: a estante dele nunca é cortada
	Client   *http.Client

	mu    sync.Mutex
	last  time.Time
	books map[string]domain.Book
}

func NewSkoob(base, token string) *Skoob {
	return &Skoob{
		Base: base, Token: token,
		Interval: 500 * time.Millisecond, PageSize: 100, MaxPages: 5,
		Client: &http.Client{Timeout: 30 * time.Second},
		books:  map[string]domain.Book{},
	}
}

// Seed ensina à fonte metadados já conhecidos (ex.: lidos do CSV), para
// que BookReaders saiba a edição sem rebaixar estantes.
func (s *Skoob) Seed(books map[string]domain.Book) {
	for _, b := range books {
		s.remember(b)
	}
}

type shelfPage struct {
	TotalPages int `json:"total_pages"`
	Items      []struct {
		Rating     float64 `json:"rating"`
		Status     string  `json:"status"`
		BookID     int64   `json:"book_id"`
		EditionID  int64   `json:"edition_id"`
		Title      string  `json:"title"`
		Author     string  `json:"author"`
		Cover      string  `json:"cover_filename"`
		FinishedAt string  `json:"finished_at"`
	} `json:"items"`
}

// UserRatings devolve a estante do usuário, com o status de cada livro
// (lido, lendo, quero ler, abandonei). Estantes de terceiros param em
// MaxPages páginas; a do Owner vem sempre inteira.
func (s *Skoob) UserRatings(ctx context.Context, userID string) ([]domain.Rating, error) {
	maxPages := s.MaxPages
	if userID == s.Owner {
		maxPages = 0
	}
	var out []domain.Rating
	for page := 1; ; page++ {
		var p shelfPage
		q := url.Values{"user_id": {userID}, "page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(s.PageSize)}}
		if err := s.get(ctx, "/v1/bookshelf", q, &p); err != nil {
			return out, err
		}
		for _, it := range p.Items {
			if it.BookID == 0 {
				continue
			}
			id := strconv.FormatInt(it.BookID, 10)
			ed := ""
			if it.EditionID != 0 {
				ed = strconv.FormatInt(it.EditionID, 10)
			}
			s.remember(domain.Book{ID: id, Title: it.Title, Author: it.Author, EditionID: ed, Cover: it.Cover})
			var at time.Time
			if it.FinishedAt != "" {
				at, _ = time.Parse(time.RFC3339, it.FinishedAt)
			}
			st := it.Status
			if st == "" {
				st = domain.StatusRead
			}
			out = append(out, domain.Rating{UserID: userID, BookID: id, Stars: it.Rating, ReadAt: at, Status: st})
		}
		if page >= p.TotalPages || len(p.Items) == 0 || (maxPages > 0 && page >= maxPages) {
			return out, nil
		}
	}
}

type ratedPage struct {
	TotalPages int `json:"total_pages"`
	Items      []struct {
		ID          string `json:"id"`
		IsMyProfile bool   `json:"is_my_profile"`
	} `json:"items"`
}

// BookReaders devolve até limit usuários que deram nota à edição do livro.
func (s *Skoob) BookReaders(ctx context.Context, bookID string, limit int) ([]string, error) {
	s.mu.Lock()
	ed := s.books[bookID].EditionID
	s.mu.Unlock()
	if ed == "" {
		return nil, fmt.Errorf("edição desconhecida para o livro %s", bookID)
	}
	var out []string
	for page := 1; limit <= 0 || len(out) < limit; page++ {
		var p ratedPage
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(s.PageSize)}}
		if err := s.get(ctx, "/v1/book/"+url.PathEscape(ed)+"/users-rated", q, &p); err != nil {
			return out, err
		}
		for _, it := range p.Items {
			if !it.IsMyProfile && it.ID != "" && (limit <= 0 || len(out) < limit) {
				out = append(out, it.ID)
			}
		}
		if page >= p.TotalPages || len(p.Items) == 0 {
			break
		}
	}
	return out, nil
}

// Book usa os metadados já vistos nas estantes (sem requisição extra).
func (s *Skoob) Book(_ context.Context, bookID string) (domain.Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.books[bookID]; ok {
		return b, nil
	}
	return domain.Book{ID: bookID, Title: bookID}, nil
}

func (s *Skoob) remember(b domain.Book) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.books[b.ID]
	if !ok {
		s.books[b.ID] = b
		return
	}
	if cur.EditionID == "" {
		cur.EditionID = b.EditionID
	}
	if cur.Cover == "" {
		cur.Cover = b.Cover
	}
	s.books[b.ID] = cur
}

// get faz GET com throttle e algumas tentativas em 429/5xx.
func (s *Skoob) get(ctx context.Context, path string, q url.Values, dst any) error {
	u := s.Base + path + "?" + q.Encode()
	for attempt := 0; ; attempt++ {
		if err := s.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", s.Token)
		req.Header.Set("Accept", "application/json")
		resp, err := s.Client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return ErrUnauthorized
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < 3:
			if err := sleepCtx(ctx, retryAfter(resp.Header.Get("Retry-After"), time.Duration(attempt+1)*5*time.Second)); err != nil {
				return err
			}
			continue
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
		}
		if err := json.Unmarshal(body, dst); err != nil {
			return fmt.Errorf("GET %s: resposta inesperada: %w", path, err)
		}
		return nil
	}
}

// retryAfter interpreta o header Retry-After (segundos ou data HTTP),
// limitado a 2 minutos; sem header válido, usa fallback.
func retryAfter(h string, fallback time.Duration) time.Duration {
	const maxWait = 2 * time.Minute
	var d time.Duration
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = time.Until(t)
	} else {
		return fallback
	}
	return min(max(d, 0), maxWait)
}

func (s *Skoob) wait(ctx context.Context) error {
	s.mu.Lock()
	d := time.Until(s.last.Add(s.Interval))
	s.last = time.Now().Add(max(d, 0))
	s.mu.Unlock()
	return sleepCtx(ctx, d)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
