// Package client is the HTTP client of a nod server (local daemon or remote).
// It implements api.Classifier. Stdlib net/http, 4 s default timeout.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MQ37/nod/internal/api"
)

// DefaultTimeout bounds every request.
const DefaultTimeout = 4 * time.Second

// Client talks to a server at BaseURL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

var _ api.Classifier = (*Client)(nil)

// New returns a client for baseURL with the given timeout (<=0 means DefaultTimeout).
func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: timeout}}
}

// ErrUnreachable wraps transport errors (connection refused, timeout, ...) so callers
// can apply the on_unreachable policy; HTTP-level failures are plain errors.
var ErrUnreachable = errors.New("server unreachable")

// Classify posts the request to /v1/classify.
func (c *Client) Classify(req api.ClassifyRequest) (api.ClassifyResponse, error) {
	var resp api.ClassifyResponse
	body, err := json.Marshal(req)
	if err != nil {
		return resp, err
	}
	hr, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.BaseURL+"/v1/classify", bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	hr.Header.Set("Content-Type", "application/json")
	r, err := c.HTTP.Do(hr)
	if err != nil {
		return resp, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return resp, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if r.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("server answered HTTP %d: %s", r.StatusCode, truncate(string(data), 200))
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, fmt.Errorf("bad server answer: %w", err)
	}
	switch resp.Decision {
	case api.Allow, api.Ask, api.Block:
	default:
		return resp, fmt.Errorf("server returned unknown decision %q", resp.Decision)
	}
	return resp, nil
}

// Health calls /healthz and returns the raw body (JSON or text) on HTTP 200.
func (c *Client) Health() (string, error) {
	return c.HealthContext(context.Background())
}

// HealthContext calls /healthz with a caller-controlled deadline.
func (c *Client) HealthContext(ctx context.Context) (string, error) {
	return c.health(ctx, false)
}

// HealthWarm calls /healthz and optionally asks the server to warm the model asynchronously.
func (c *Client) HealthWarm(warm bool) (string, error) {
	return c.HealthWarmContext(context.Background(), warm)
}

// HealthWarmContext calls /healthz with an optional warm request and caller-controlled deadline.
func (c *Client) HealthWarmContext(ctx context.Context, warm bool) (string, error) {
	return c.health(ctx, warm)
}

func (c *Client) health(ctx context.Context, warm bool) (string, error) {
	url := c.BaseURL + "/healthz"
	if warm {
		url += "?warm=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	r, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if r.StatusCode != http.StatusOK {
		return "", fmt.Errorf("healthz answered HTTP %d", r.StatusCode)
	}
	return strings.TrimSpace(string(data)), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// PolicyDecision maps the on_unreachable policy (ask|block|allow) to a decision;
// anything unknown fails safe to ask.
func PolicyDecision(policy string) api.Decision {
	switch policy {
	case "block":
		return api.Block
	case "allow":
		return api.Allow
	}
	return api.Ask
}

// ReviewPrompt asks a nod server for the prompt a host plugin sends to the agent's own model.
func (c *Client) ReviewPrompt(req api.ClassifyRequest) (api.ReviewPrompt, error) {
	var out api.ReviewPrompt
	err := c.postJSON("/v1/review/prompt", req, &out)
	return out, err
}

// ReviewVerdict sends the model's answers to the server's pipeline and returns the decision.
func (c *Client) ReviewVerdict(req api.ClassifyRequest, answers map[string]any) (api.ClassifyResponse, error) {
	var out api.ClassifyResponse
	err := c.postJSON("/v1/review/verdict", api.ReviewVerdictRequest{Request: req, Answers: answers}, &out)
	return out, err
}

func (c *Client) postJSON(path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	hr, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hr.Header.Set("Content-Type", "application/json")
	r, err := c.HTTP.Do(hr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("server answered HTTP %d: %s", r.StatusCode, truncate(string(data), 200))
	}
	return json.Unmarshal(data, out)
}
