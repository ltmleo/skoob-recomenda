package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
	"github.com/ltmleo/skoob-recomenda/internal/recommend"
	"github.com/ltmleo/skoob-recomenda/internal/source"
)

func newTestServer(t *testing.T, src source.Source, anyHost bool) *Server {
	t.Helper()
	mem := source.NewMemory()
	mem.Add(rt("me", "duna", 5, "read"))
	mem.Add(rt("a", "duna", 5, "read"))
	mem.MergeBook(domain.Book{ID: "duna", Title: "Duna", EditionID: "9"})
	return New(context.Background(), Config{Me: "me", DataPath: filepath.Join(t.TempDir(), "s.csv"), Mem: mem,
		Src: src, Rec: recommend.DefaultOptions(), N: 5, AnyHost: anyHost})
}

func do(h http.Handler, method, target string, hdr map[string]string) int {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

func TestRejectsForeignHost(t *testing.T) {
	h := newTestServer(t, nil, false).Handler()
	for host, want := range map[string]int{
		"127.0.0.1:8080":         http.StatusOK,
		"localhost:8080":         http.StatusOK,
		"[::1]:8080":             http.StatusOK,
		"evil.example:8080":      http.StatusMisdirectedRequest, // DNS rebinding
		"192.168.0.10:8080":      http.StatusMisdirectedRequest,
		"127.0.0.1.evil.example": http.StatusMisdirectedRequest,
	} {
		if got := do(h, "GET", "/api/state", map[string]string{"Host": host}); got != want {
			t.Errorf("Host %s: got %d, want %d", host, got, want)
		}
	}
	if got := do(newTestServer(t, nil, true).Handler(), "GET", "/api/state", map[string]string{"Host": "casa.lan:8080"}); got != 200 {
		t.Errorf("AnyHost deveria aceitar qualquer host, got %d", got)
	}
}

func TestRejectsCrossSitePost(t *testing.T) {
	h := newTestServer(t, source.NewMemory(), false).Handler()
	local := map[string]string{"Host": "127.0.0.1:8080"}
	cross := map[string]string{"Host": "127.0.0.1:8080", "Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
	if got := do(h, "POST", "/api/expand?id=duna", cross); got != http.StatusForbidden {
		t.Fatalf("POST de outro site deveria ser barrado (CSRF), got %d", got)
	}
	oldBrowser := map[string]string{"Host": "127.0.0.1:8080", "Origin": "https://evil.example"}
	if got := do(h, "POST", "/api/expand?id=duna", oldBrowser); got != http.StatusForbidden {
		t.Fatalf("Origin diferente sem Sec-Fetch-Site também, got %d", got)
	}
	if got := do(h, "POST", "/api/expand", local); got != http.StatusBadRequest {
		t.Fatalf("sem id deveria dar 400, got %d", got)
	}
	same := map[string]string{"Host": "127.0.0.1:8080", "Sec-Fetch-Site": "same-origin"}
	if got := do(h, "POST", "/api/expand?id=duna", same); got != http.StatusAccepted {
		t.Fatalf("POST da própria página deveria passar, got %d", got)
	}
}

// slow só devolve estantes quando o contexto é cancelado.
type slow struct{ *source.Memory }

func (s slow) BookReaders(context.Context, string, int) ([]string, error) {
	return []string{"x1", "x2", "x3"}, nil
}

func (s slow) UserRatings(ctx context.Context, u string) ([]domain.Rating, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCancelExpansion(t *testing.T) {
	s := newTestServer(t, slow{source.NewMemory()}, false)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	send := func(method string) int {
		req, _ := http.NewRequest(method, srv.URL+"/api/expand?id=duna", nil)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := send("DELETE"); c != http.StatusConflict {
		t.Fatalf("cancelar sem expansão: %d", c)
	}
	if c := send("POST"); c != http.StatusAccepted {
		t.Fatalf("expand: %d", c)
	}
	if c := send("DELETE"); c != http.StatusAccepted {
		t.Fatalf("cancelar: %d", c)
	}
	var job Job
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r, err := http.Get(srv.URL + "/api/job")
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(r.Body).Decode(&job)
		r.Body.Close()
		if !job.Running {
			break
		}
	}
	if job.Running || !job.Canceled || job.Error != "" {
		t.Fatalf("job deveria terminar como cancelado: %+v", job)
	}
	if c := send("POST"); c != http.StatusAccepted {
		t.Fatalf("depois de cancelar, nova expansão deveria poder começar: %d", c)
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:0": true, "[::1]:80": true, "127.0.0.2:1": true,
		"0.0.0.0:8080": false, ":8080": false, "192.168.1.2:80": false,
	} {
		if got := IsLoopback(addr); got != want {
			t.Errorf("%s: got %v", addr, got)
		}
	}
}
