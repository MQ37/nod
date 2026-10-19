package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decider"
)

type testModel struct{}

func (testModel) Decide(string, decider.Schema) (map[string]decider.Answer, error) {
	return map[string]decider.Answer{}, nil
}
func (testModel) Close() error { return nil }

func TestHealthWarmStartsBackgroundLoad(t *testing.T) {
	gate := make(chan struct{})
	m := decider.NewManaged(func(context.Context) (decider.ClosableDecider, error) { <-gate; return testModel{}, nil }, time.Second, time.Hour)
	r := httptest.NewRequest("GET", "/healthz?warm=1", nil)
	w := httptest.NewRecorder()
	Handler(nil, m).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := m.Status().Status; got != "loading" {
		t.Fatalf("model status=%s", got)
	}
	close(gate)
}

type slowClassifier struct{}

func (slowClassifier) Classify(api.ClassifyRequest) (api.ClassifyResponse, error) {
	time.Sleep(20 * time.Millisecond)
	return api.ClassifyResponse{Decision: api.Allow, Source: "model"}, nil
}

func TestClassifyReportsElapsedMs(t *testing.T) {
	srv := httptest.NewServer(Handler(slowClassifier{}, nil))
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/classify", "application/json", strings.NewReader(`{"tool":"Bash","input":"ls"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out api.ClassifyResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Ms < 15 {
		t.Fatalf("ms=%d, want the real elapsed time (>= 15)", out.Ms)
	}
}
