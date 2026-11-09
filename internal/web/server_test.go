package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

func rt(u, b string, s float64, st string) domain.Rating {
	return domain.Rating{UserID: u, BookID: b, Stars: s, Status: st}
}

func TestServerStateBookAndExpand(t *testing.T) {
	mem := source.NewMemory()
	for _, r := range []domain.Rating{
		rt("me", "duna", 5, "read"), rt("me", "fundacao", 0, "want_to_read"), rt("me", "chato", 2, "abandoned"),
		rt("a", "duna", 5, "read"), rt("a", "fundacao", 5, "read"),
		rt("b", "duna", 4, "read"), rt("b", "fundacao", 4, "read"),
	} {
		mem.Add(r)
	}
	mem.MergeBook(domain.Book{ID: "duna", Title: "Duna", EditionID: "9"})

	// Fonte remota simulada: um leitor novo de duna, que também ama "neuromancer".
	remote := source.NewMemory()
	for _, r := range []domain.Rating{
		rt("a", "duna", 5, "read"), rt("novo", "duna", 5, "read"), rt("novo", "neuromancer", 5, "read"),
	} {
		remote.Add(r)
	}
	path := filepath.Join(t.TempDir(), "skoob.csv")
	o := recommend.DefaultOptions()
	o.MinOverlap = 1
	s := New(context.Background(), Config{Me: "me", DataPath: path, Mem: mem, Src: remote, Rec: o, N: 10})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	get := func(p string, v any) {
		t.Helper()
		r, err := http.Get(srv.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", p, err, r.Status)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}

	var st struct {
		Shelf []struct {
			Book   struct{ ID string }
			Status string
		}
		CanExpand bool
	}
	get("/api/state", &st)
	if len(st.Shelf) != 3 || !st.CanExpand {
		t.Fatalf("estante: %+v", st)
	}

	var bk struct {
		Book    struct{ URL string }
		Likers  int
		Similar []struct {
			Book struct{ ID string }
			Mine *struct{ Status string }
		}
	}
	get("/api/book?id=duna", &bk)
	if bk.Likers != 3 || bk.Book.URL == "" || len(bk.Similar) != 1 || bk.Similar[0].Book.ID != "fundacao" ||
		bk.Similar[0].Mine == nil || bk.Similar[0].Mine.Status != "want_to_read" {
		t.Fatalf("livro: %+v", bk)
	}

	r, err := http.Post(srv.URL+"/api/expand?id=duna&readers=10", "", nil)
	if err != nil || r.StatusCode != http.StatusAccepted {
		t.Fatalf("expand: %v %v", err, r.Status)
	}
	var job Job
	for deadline := time.Now().Add(5 * time.Second); ; {
		get("/api/job", &job)
		if !job.Running || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Running || job.Added != 1 || job.Error != "" {
		t.Fatalf("job: %+v", job)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "neuromancer") {
		t.Fatal("CSV deveria ser regravado com a estante nova")
	}
}

func TestGraphAndExpansionHistory(t *testing.T) {
	mem := source.NewMemory()
	for _, r := range []domain.Rating{
		rt("me", "a", 5, "read"), rt("me", "b", 4, "read"), rt("me", "c", 0, "want_to_read"),
		rt("u1", "a", 5, "read"), rt("u1", "b", 2, "read"),
		rt("u2", "a", 4, "read"), rt("u2", "b", 5, "read"), rt("u2", "c", 5, "read"),
	} {
		mem.Add(r)
	}
	remote := source.NewMemory()
	remote.Add(rt("u3", "a", 5, "read"))
	remote.Add(rt("u3", "c", 5, "read"))
	path := filepath.Join(t.TempDir(), "skoob.csv")
	s := New(context.Background(), Config{Me: "me", DataPath: path, Mem: mem, Src: remote, Rec: recommend.DefaultOptions(), N: 5})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	type graph struct {
		Nodes []struct {
			ID       string
			Readers  int
			Expanded bool
		}
		Edges []struct {
			A, B string
			W    int
		}
		Version int
	}
	fetch := func(q string) graph {
		var g graph
		r, err := http.Get(srv.URL + "/api/graph" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&g)
		return g
	}
	w := func(g graph, a, b string) int {
		for _, e := range g.Edges {
			if e.A == a && e.B == b {
				return e.W
			}
		}
		return 0
	}
	g := fetch("")
	if len(g.Nodes) != 3 || w(g, "a", "b") != 2 || w(g, "a", "c") != 1 {
		t.Fatalf("grafo: %+v", g)
	}
	if gl := fetch("?liked=1"); w(gl, "a", "b") != 1 {
		t.Fatalf("liked: só u2 gostou de a e b: %+v", gl.Edges)
	}

	if r, _ := http.Post(srv.URL+"/api/expand?id=a&readers=10", "", nil); r.StatusCode != http.StatusAccepted {
		t.Fatal(r.Status)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if fetch("").Version == 1 {
			break
		}
	}
	g = fetch("")
	if g.Version != 1 || w(g, "a", "c") != 2 {
		t.Fatalf("após expandir, versão 1 e a–c com 2 leitores: %+v", g)
	}
	for _, n := range g.Nodes {
		if n.ID == "a" && !n.Expanded {
			t.Fatal("nó a deveria estar marcado como expandido")
		}
	}
	// O histórico persiste: um servidor novo lê o arquivo.
	s2 := New(context.Background(), Config{Me: "me", DataPath: path, Mem: mem, Rec: recommend.DefaultOptions()})
	if len(s2.expansions) != 1 || s2.expansions[0].BookID != "a" || s2.expansions[0].Added != 1 {
		t.Fatalf("histórico: %+v", s2.expansions)
	}
}
