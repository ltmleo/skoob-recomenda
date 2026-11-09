// Package web serve o app local: recomendações, estante e "seguir um livro"
// (navegar pelo grafo livro -> quem gostou -> o que mais gostaram).
package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/crawl"
	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

//go:embed index.html
var indexHTML []byte

// Config do servidor.
type Config struct {
	Me       string         // ID do usuário-alvo
	DataPath string         // CSV lido e regravado após cada expansão
	Mem      *source.Memory // dados já carregados de DataPath
	Src      source.Source  // nil = sem expansão (sem token)
	Rec      recommend.Options
	N        int // recomendações

	// AnyHost desliga a checagem do header Host. Só para quando o serve
	// escuta fora do loopback de propósito (ex.: 0.0.0.0 na rede de casa).
	AnyHost bool
}

type Server struct {
	cfg Config
	ctx context.Context

	mu         sync.RWMutex
	mem        *source.Memory
	recs       recommend.Result
	job        Job
	cancel     context.CancelFunc // cancela a expansão em andamento
	version    int                // muda a cada expansão concluída: as páginas recarregam
	expansions []Expansion        // histórico persistido ao lado do CSV
}

// Job é a expansão do grafo em andamento (uma por vez).
type Job struct {
	BookID   string `json:"bookId"`
	Title    string `json:"title"`
	Running  bool   `json:"running"`
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Added    int    `json:"added"`
	Canceled bool   `json:"canceled,omitempty"`
	Error    string `json:"error,omitempty"`
	Version  int    `json:"version"`
}

// Expansion registra uma expansão do grafo já feita.
type Expansion struct {
	BookID  string    `json:"bookId"`
	Title   string    `json:"title"`
	At      time.Time `json:"at"`
	Readers int       `json:"readers"` // pedidos
	Added   int       `json:"added"`   // estantes novas
}

// New cria o servidor; ctx encerra expansões em andamento.
func New(ctx context.Context, cfg Config) *Server {
	s := &Server{cfg: cfg, ctx: ctx, mem: cfg.Mem}
	s.recs = recommend.Recommend(s.mem.Ratings, cfg.Me, cfg.N, s.recOpts())
	if b, err := os.ReadFile(s.expansionsPath()); err == nil {
		_ = json.Unmarshal(b, &s.expansions)
	}
	return s
}

func (s *Server) expansionsPath() string { return s.cfg.DataPath + ".expansoes.json" }

// saveExpansions grava o histórico (chamar com s.mu travado).
func (s *Server) saveExpansions() error {
	b, err := json.MarshalIndent(s.expansions, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.expansionsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.expansionsPath())
}

// recOpts são as opções do algoritmo (chamar com s.mu travado: AuthorOf lê s.mem).
func (s *Server) recOpts() recommend.Options {
	o := s.cfg.Rec
	o.Now = time.Now()
	o.AuthorOf = func(id string) string { return s.mem.Books[id].Author }
	return o
}

// IsLoopback informa se addr (host:porta) escuta só na própria máquina.
func IsLoopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		h = addr
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Handler devolve as rotas protegidas contra dois ataques de sites abertos
// no mesmo navegador:
//   - DNS rebinding: um domínio do atacante passa a apontar para 127.0.0.1 e
//     lê /api/state (a sua estante). Barrado conferindo o header Host.
//   - CSRF: um site dispara POST /api/expand e gasta o seu token do Skoob.
//     Barrado por http.CrossOriginProtection (Sec-Fetch-Site / Origin).
func (s *Server) Handler() http.Handler {
	h := http.NewCrossOriginProtection().Handler(s.routes())
	if s.cfg.AnyHost {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsLoopback(r.Host) {
			http.Error(w, "host não permitido", http.StatusMisdirectedRequest)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/book", s.book)
	mux.HandleFunc("POST /api/expand", s.expand)
	mux.HandleFunc("DELETE /api/expand", s.cancelExpand)
	mux.HandleFunc("GET /api/graph", s.graph)
	mux.HandleFunc("GET /api/job", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		j := s.job
		j.Version = s.version
		writeJSON(w, j)
	})
	return mux
}

// --- views ---

type bookView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author,omitempty"`
	Cover  string `json:"cover,omitempty"`
	URL    string `json:"url,omitempty"`
}

type mine struct {
	Status string  `json:"status"`
	Stars  float64 `json:"stars"`
	ReadAt string  `json:"readAt,omitempty"`
}

func (s *Server) bv(id string) bookView {
	b, ok := s.mem.Books[id]
	if !ok {
		b = domain.Book{ID: id, Title: id}
	}
	v := bookView{ID: id, Title: b.Title, Author: b.Author, Cover: b.Cover}
	if b.EditionID != "" {
		v.URL = "https://www.skoob.com.br/pt/book/" + b.EditionID
	}
	return v
}

// myShelf indexa a estante do usuário por livro.
func (s *Server) myShelf() map[string]mine {
	out := map[string]mine{}
	for _, r := range s.mem.Ratings[s.cfg.Me] {
		st := r.Status
		if st == "" {
			st = domain.StatusRead
		}
		m := mine{Status: st, Stars: r.Stars}
		if !r.ReadAt.IsZero() {
			m.ReadAt = r.ReadAt.Format("2006-01-02")
		}
		out[r.BookID] = m
	}
	return out
}

func (s *Server) state(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	shelf := s.myShelf()

	type evidence struct {
		Stars float64 `json:"stars"`
		Sim   float64 `json:"sim"`
	}
	type bookEvidence struct {
		Book  bookView `json:"book"`
		Stars float64  `json:"stars"`
		Sim   float64  `json:"sim"`
	}
	type recView struct {
		Book        bookView       `json:"book"`
		Predicted   float64        `json:"predicted"`
		Support     int            `json:"support"`
		ItemSupport int            `json:"itemSupport"`
		Because     []evidence     `json:"because"`
		BecauseOf   []bookEvidence `json:"becauseOf"`
		Mine        *mine          `json:"mine,omitempty"`
	}
	recs := []recView{}
	for _, r := range s.recs.Recommendations {
		v := recView{Book: s.bv(r.BookID), Predicted: r.Predicted, Support: r.Support, ItemSupport: r.ItemSupport,
			Because: []evidence{}, BecauseOf: []bookEvidence{}}
		for _, e := range r.Because {
			v.Because = append(v.Because, evidence{Stars: e.Stars, Sim: e.Sim})
		}
		for _, e := range r.BecauseOf {
			v.BecauseOf = append(v.BecauseOf, bookEvidence{Book: s.bv(e.BookID), Stars: e.Stars, Sim: e.Sim})
		}
		if m, ok := shelf[r.BookID]; ok {
			v.Mine = &m
		}
		recs = append(recs, v)
	}

	type shelfItem struct {
		Book bookView `json:"book"`
		mine
	}
	items := []shelfItem{}
	for id, m := range shelf {
		items = append(items, shelfItem{Book: s.bv(id), mine: m})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ReadAt != items[j].ReadAt {
			return items[i].ReadAt > items[j].ReadAt
		}
		return items[i].Book.Title < items[j].Book.Title
	})

	writeJSON(w, map[string]any{
		"me":         s.cfg.Me,
		"users":      len(s.mem.Ratings),
		"books":      len(s.mem.Readers),
		"neighbors":  len(s.recs.Neighbors),
		"recs":       recs,
		"shelf":      items,
		"canExpand":  s.cfg.Src != nil,
		"job":        s.job,
		"version":    s.version,
		"expansions": s.expansions,
	})
}

// graph monta o grafo dos seus livros: nós são livros da sua estante e uma
// aresta A–B conta quantos outros leitores do grafo têm A e B. Com
// liked=1, só conta quem deu nota >= 4 aos dois.
func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	liked := r.URL.Query().Get("liked") == "1"
	s.mu.RLock()
	defer s.mu.RUnlock()

	shelf := s.myShelf()
	expanded := map[string]bool{}
	for _, e := range s.expansions {
		expanded[e.BookID] = true
	}
	counts := map[string]int{}   // livro -> leitores (outros)
	edges := map[[2]string]int{} // par ordenado -> leitores em comum
	for uid, rs := range s.mem.Ratings {
		if uid == s.cfg.Me {
			continue
		}
		var mine []string
		seen := map[string]bool{}
		for _, x := range rs {
			if _, ok := shelf[x.BookID]; !ok || seen[x.BookID] || !x.Rated() {
				continue
			}
			if liked && x.Stars < recommend.LikeThreshold {
				continue
			}
			seen[x.BookID] = true
			mine = append(mine, x.BookID)
		}
		sort.Strings(mine)
		for i, a := range mine {
			counts[a]++
			for _, b := range mine[i+1:] {
				edges[[2]string{a, b}]++
			}
		}
	}

	type node struct {
		bookView
		mine
		Readers  int  `json:"readers"`
		Expanded bool `json:"expanded"`
	}
	type edge struct {
		A string `json:"a"`
		B string `json:"b"`
		W int    `json:"w"`
	}
	nodes := []node{}
	for id, m := range shelf {
		nodes = append(nodes, node{bookView: s.bv(id), mine: m, Readers: counts[id], Expanded: expanded[id]})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	es := make([]edge, 0, len(edges))
	for k, n := range edges {
		es = append(es, edge{A: k[0], B: k[1], W: n})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].W > es[j].W })
	writeJSON(w, map[string]any{"nodes": nodes, "edges": es, "version": s.version, "liked": liked})
}

func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id obrigatório", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	shelf := s.myShelf()
	res := recommend.Similar(s.mem.Ratings, id, 60, 2, 2)

	type simView struct {
		Book    bookView `json:"book"`
		Score   float64  `json:"score"`
		CoLikes int      `json:"coLikes"`
		Likers  int      `json:"likers"`
		Mine    *mine    `json:"mine,omitempty"`
	}
	sims := []simView{}
	for _, x := range res.Similar {
		v := simView{Book: s.bv(x.BookID), Score: x.Score, CoLikes: x.CoLikes, Likers: x.Likers}
		if m, ok := shelf[x.BookID]; ok {
			v.Mine = &m
		}
		sims = append(sims, v)
	}
	out := map[string]any{
		"book":    s.bv(id),
		"likers":  res.Likers,
		"raters":  res.Raters,
		"similar": sims,
	}
	if m, ok := shelf[id]; ok {
		out["mine"] = m
	}
	writeJSON(w, out)
}

func (s *Server) expand(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	readers, _ := strconv.Atoi(r.URL.Query().Get("readers"))
	if readers <= 0 || readers > 200 {
		readers = 40
	}
	if id == "" {
		http.Error(w, "id obrigatório", http.StatusBadRequest)
		return
	}
	if s.cfg.Src == nil {
		http.Error(w, "sem login no Skoob: rode `recomenda login` e reinicie o serve", http.StatusConflict)
		return
	}
	s.mu.Lock()
	if s.job.Running {
		s.mu.Unlock()
		http.Error(w, "já existe uma expansão em andamento", http.StatusConflict)
		return
	}
	s.job = Job{BookID: id, Title: s.bv(id).Title, Running: true}
	// Expande numa Memory temporária: a leitura do app segue livre enquanto
	// as estantes chegam. As chaves pré-preenchidas marcam quem já temos.
	tmp := source.NewMemory()
	for u := range s.mem.Ratings {
		tmp.Ratings[u] = nil
	}
	for bid, b := range s.mem.Books {
		tmp.Books[bid] = b
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.mu.Unlock()

	go s.runExpand(ctx, id, readers, tmp)
	writeJSONStatus(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// cancelExpand interrompe a expansão em andamento; o que já foi baixado é salvo.
func (s *Server) cancelExpand(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.job.Running || s.cancel == nil {
		http.Error(w, "nenhuma expansão em andamento", http.StatusConflict)
		return
	}
	s.cancel()
	writeJSONStatus(w, http.StatusAccepted, map[string]string{"status": "canceling"})
}

func (s *Server) runExpand(ctx context.Context, id string, readers int, tmp *source.Memory) {
	added, err := crawl.ExpandBook(ctx, s.cfg.Src, tmp, id, readers, func(done, total int) {
		s.mu.Lock()
		s.job.Done, s.job.Total = done, total
		s.mu.Unlock()
	})

	s.mu.Lock()
	for u, rs := range tmp.Ratings {
		if len(rs) == 0 {
			continue
		}
		if _, ok := s.mem.Ratings[u]; ok {
			continue
		}
		for _, x := range rs {
			s.mem.Add(x)
		}
	}
	for _, b := range tmp.Books {
		s.mem.MergeBook(b)
	}
	if added > 0 {
		s.recs = recommend.Recommend(s.mem.Ratings, s.cfg.Me, s.cfg.N, s.recOpts())
		if serr := saveAtomic(s.cfg.DataPath, s.mem); serr != nil && err == nil {
			err = serr
		}
	}
	if err == nil || added > 0 {
		s.expansions = append(s.expansions, Expansion{BookID: id, Title: s.job.Title, At: time.Now(), Readers: readers, Added: added})
		if serr := s.saveExpansions(); serr != nil && err == nil {
			err = serr
		}
		s.version++
	}
	s.job.Running = false
	s.job.Added = added
	switch {
	case errors.Is(err, context.Canceled):
		s.job.Canceled = s.ctx.Err() == nil // cancelado pelo usuário, não pelo encerramento
	case err != nil:
		s.job.Error = err.Error()
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
}

func saveAtomic(path string, mem *source.Memory) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".skoob-*.csv")
	if err != nil {
		return err
	}
	if err := mem.WriteCSV(tmp); err != nil {
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

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
