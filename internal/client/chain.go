package client

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/MQ37/nod/internal/api"
)

// Chain asks the remote server first and falls back to the local daemon when the remote cannot
// answer. A remote that failed is skipped for downFor, so a hook burst while you are offline
// pays the connect timeout once, not once per call. The local daemon decides for itself whether
// its model may run (for example not on battery); if it cannot answer either, Classify returns
// the error and the caller applies on_unreachable.
type Chain struct {
	Remote    *Client
	Local     *Client
	StateFile string // remembers a failed remote across the short-lived hook processes; "" = off
	Now       func() time.Time
	downFor   time.Duration
}

var _ api.Classifier = (*Chain)(nil)

const (
	remoteDownFor  = 15 * time.Second
	remoteDialWait = time.Second
)

// NewChain builds the fallback chain. The remote gets a short dial timeout so an unreachable
// host (a sleeping tailnet, no Wi-Fi) fails fast.
func NewChain(remoteURL, localURL string, timeout time.Duration, stateFile string) *Chain {
	remote := New(remoteURL, timeout)
	remote.HTTP.Transport = &http.Transport{DialContext: (&net.Dialer{Timeout: remoteDialWait}).DialContext}
	return &Chain{Remote: remote, Local: New(localURL, timeout), StateFile: stateFile, Now: time.Now, downFor: remoteDownFor}
}

func (c *Chain) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

type downState struct {
	Until int64 `json:"until"`
}

func (c *Chain) remoteDown() bool {
	if c.StateFile == "" {
		return false
	}
	b, err := os.ReadFile(c.StateFile)
	if err != nil {
		return false
	}
	var s downState
	return json.Unmarshal(b, &s) == nil && c.now().Unix() < s.Until
}

func (c *Chain) markRemote(ok bool) {
	if c.StateFile == "" {
		return
	}
	if ok {
		_ = os.Remove(c.StateFile)
		return
	}
	_ = os.MkdirAll(filepath.Dir(c.StateFile), 0o700)
	b, _ := json.Marshal(downState{Until: c.now().Add(c.downFor).Unix()})
	_ = os.WriteFile(c.StateFile, b, 0o600)
}

// Classify implements api.Classifier.
func (c *Chain) Classify(req api.ClassifyRequest) (api.ClassifyResponse, error) {
	if !c.remoteDown() {
		resp, err := c.Remote.Classify(req)
		if err == nil {
			c.markRemote(true)
			resp.Via = "remote"
			return resp, nil
		}
		c.markRemote(false)
	}
	resp, err := c.Local.Classify(req)
	if err != nil {
		return resp, errors.Join(ErrUnreachable, err)
	}
	resp.Via = "local"
	return resp, nil
}

// HealthWarm asks the remote to warm its model; when the remote is down it warms the local
// daemon instead (which refuses on battery).
func (c *Chain) HealthWarm(warm bool) (string, error) {
	if !c.remoteDown() {
		if out, err := c.Remote.HealthWarm(warm); err == nil {
			c.markRemote(true)
			return out, nil
		}
		c.markRemote(false)
	}
	return c.Local.HealthWarm(warm)
}
