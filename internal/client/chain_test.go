package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/api"
)

func server(t *testing.T, decision api.Decision, status int, hits *int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if status != 200 {
			http.Error(w, "down", status)
			return
		}
		_ = json.NewEncoder(w).Encode(api.ClassifyResponse{Decision: decision, Source: "model"})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestChainPrefersRemote(t *testing.T) {
	var rh, lh int32
	r, l := server(t, api.Allow, 200, &rh), server(t, api.Block, 200, &lh)
	c := NewChain(r.URL, l.URL, time.Second, "")
	got, err := c.Classify(api.ClassifyRequest{Tool: "Bash", Input: "ls"})
	if err != nil || got.Decision != api.Allow || got.Via != "remote" || lh != 0 {
		t.Fatalf("%+v err=%v localHits=%d", got, err, lh)
	}
}

func TestChainFallsBackAndRemembersDownRemote(t *testing.T) {
	var rh, lh int32
	r, l := server(t, api.Allow, 503, &rh), server(t, api.Ask, 200, &lh)
	state := filepath.Join(t.TempDir(), "down.json")
	now := time.Unix(1000, 0)
	c := NewChain(r.URL, l.URL, time.Second, state)
	c.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		got, err := c.Classify(api.ClassifyRequest{Tool: "Bash", Input: "ls"})
		if err != nil || got.Decision != api.Ask || got.Via != "local" {
			t.Fatalf("call %d: %+v err=%v", i, got, err)
		}
	}
	if rh != 1 {
		t.Fatalf("remote was tried %d times; a failed remote must be skipped for a while", rh)
	}
	now = now.Add(20 * time.Second) // the quiet period is over: try the remote again
	if _, err := c.Classify(api.ClassifyRequest{Tool: "Bash", Input: "ls"}); err != nil || rh != 2 {
		t.Fatalf("remote retried=%d err=%v", rh, err)
	}
}

func TestChainBothDownIsUnreachable(t *testing.T) {
	var rh, lh int32
	r, l := server(t, api.Allow, 503, &rh), server(t, api.Allow, 503, &lh)
	c := NewChain(r.URL, l.URL, time.Second, "")
	if _, err := c.Classify(api.ClassifyRequest{Tool: "Bash", Input: "ls"}); err == nil {
		t.Fatal("want an error so the caller applies on_unreachable")
	}
}
