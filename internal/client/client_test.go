package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthWarmRequestsEndpoint(t *testing.T) {
	paths := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL, time.Second)
	if _, err := c.HealthWarm(true); err != nil {
		t.Fatal(err)
	}
	if got := <-paths; got != "/healthz?warm=1" {
		t.Fatalf("warm path %q", got)
	}
	if _, err := c.Health(); err != nil {
		t.Fatal(err)
	}
	if got := <-paths; got != "/healthz" {
		t.Fatalf("health path %q", got)
	}
}
