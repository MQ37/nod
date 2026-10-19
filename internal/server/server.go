// Package server implements the local HTTP API.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decider"
)

const maxBody = 64 << 10

// Handler returns the HTTP API. A nil classifier/decider is handled fail-safe.
func Handler(classifier api.Classifier, d decider.Decider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("warm") == "1" {
			if m, ok := d.(interface{ Warm() }); ok {
				m.Warm()
			}
		}
		body := map[string]any{"ok": true}
		if m, ok := d.(interface{ Status() decider.State }); ok {
			s := m.Status()
			body["model_status"] = s.Status
			body["idle_seconds"] = s.IdleSeconds
			if s.Paused != "" {
				body["paused"] = s.Paused
			}
		}
		writeJSON(w, http.StatusOK, body)
	})
	mux.HandleFunc("POST /v1/classify", func(w http.ResponseWriter, r *http.Request) {
		var req api.ClassifyRequest
		if !decode(w, r, &req) {
			return
		}
		if classifier == nil {
			writeClassifyError(w)
			return
		}
		start := time.Now()
		resp, err := classifier.Classify(req)
		if err != nil {
			writeClassifyError(w)
			return
		}
		resp.Ms = int(time.Since(start).Milliseconds())
		writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("POST /v1/review/prompt", func(w http.ResponseWriter, r *http.Request) {
		rv, ok := classifier.(api.Reviewer)
		if !ok {
			http.Error(w, "review is not available", http.StatusNotImplemented)
			return
		}
		var req api.ClassifyRequest
		if !decode(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, rv.ReviewPrompt(req))
	})
	mux.HandleFunc("POST /v1/review/verdict", func(w http.ResponseWriter, r *http.Request) {
		rv, ok := classifier.(api.Reviewer)
		if !ok {
			http.Error(w, "review is not available", http.StatusNotImplemented)
			return
		}
		var body api.ReviewVerdictRequest
		if !decode(w, r, &body) {
			return
		}
		resp, err := rv.ReviewVerdict(body.Request, body.Answers)
		if err != nil {
			http.Error(w, "invalid answers: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("POST /decide", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Context string `json:"context"`
			Schema  Schema `json:"schema"`
		}
		if !decode(w, r, &req) {
			return
		}
		if d == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "decider unavailable"})
			return
		}
		ans, err := d.Decide(req.Context, req.Schema)
		if err != nil {
			msg := "decider unavailable"
			if errors.Is(err, decider.ErrWarmingUp) {
				msg = "model warming up"
			} else if errors.Is(err, decider.ErrPaused) {
				msg = err.Error()
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": msg})
			return
		}
		b, err := decider.MarshalAnswers(req.Schema, ans)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid decider result"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, bytes.NewReader(b))
	})
	return mux
}

// NewServer builds a server with conservative timeouts.
func NewServer(addr string, classifier api.Classifier, d decider.Decider) *http.Server {
	return &http.Server{Addr: addr, Handler: Handler(classifier, d), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
}

type Schema = decider.Schema

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if r.Body == nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	return true
}
func writeClassifyError(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, api.ClassifyResponse{Decision: api.Ask, Source: "none", Rules: []string{}, Error: "classification unavailable"})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
