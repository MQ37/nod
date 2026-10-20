package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decisionlog"
	"github.com/MQ37/nod/internal/hosts"
)

func testAdapter(t *testing.T) *Adapter {
	t.Helper()
	home := t.TempDir()
	env := hosts.Env{Home: home, Getenv: func(string) string { return "" }, Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }, Stderr: &bytes.Buffer{}}
	return New(env)
}
func TestInstallBackupIdempotentAndRoundTrip(t *testing.T) {
	a := testAdapter(t)
	p := a.SettingsPath()
	original := []byte("{\n  \"other\": [1, 2]\n}\n")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Steps[0].Diff, `"matcher": "Bash"`) || !strings.Contains(string(plan.Steps[0].Content), `"timeout": 15`) {
		t.Fatalf("wrong hook: %s", plan.Steps[0].Diff)
	}
	if err = a.Install(plan); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(p + ".bak-nod-20260102-030405"); err != nil {
		t.Fatal(err)
	}
	again, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Changes() {
		t.Fatal("install not idempotent")
	}
	rp, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(rp); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("round trip changed bytes:\n%s", got)
	}
}
func TestMalformedSettingsRefused(t *testing.T) {
	a := testAdapter(t)
	p := a.SettingsPath()
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte("{bad"), 0600)
	_, err := a.PlanInstall(hosts.Options{})
	if hosts.CodeOf(err) != 4 {
		t.Fatalf("expected exit 4, got %v", err)
	}
}

func TestReasonOmitsMissingProbabilities(t *testing.T) {
	allow := 0.75
	for _, tc := range []struct {
		name string
		resp api.ClassifyResponse
		want string
	}{
		{name: "tier", resp: api.ClassifyResponse{Decision: api.Allow, Source: "tier"}, want: "nod: allow (tier)"},
		{name: "one probability", resp: api.ClassifyResponse{Decision: api.Ask, Source: "model", PAllow: &allow}, want: "nod: ask (model); p_allow=0.75"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reason(tc.resp); got != tc.want {
				t.Fatalf("reason=%q want %q", got, tc.want)
			}
		})
	}
}

type warmClassifier struct{ called bool }

func (w *warmClassifier) Classify(api.ClassifyRequest) (api.ClassifyResponse, error) {
	return api.ClassifyResponse{}, nil
}
func (w *warmClassifier) HealthWarm(v bool) (string, error) { w.called = v; return "", nil }

func TestSessionStartWarmIsOptInAndRemoved(t *testing.T) {
	a := testAdapter(t)
	p, err := a.PlanInstall(hosts.Options{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Steps[0].Content), `"SessionStart"`) || !strings.Contains(string(p.Steps[0].Content), `hook claude --warm`) {
		t.Fatalf("warm hook missing: %s", p.Steps[0].Content)
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	if _, obs, err := state(mustRead(t, a.SettingsPath())); err != nil || !obs {
		t.Fatalf("warm hook not detected: obs=%t err=%v", obs, err)
	}
	w := &warmClassifier{}
	a.Hook(strings.NewReader(`{"hook_event_name":"SessionStart"}`), &bytes.Buffer{}, w)
	if !w.called {
		t.Fatal("session start did not request warm")
	}
	rp, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(rp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, a.SettingsPath())), `--warm`) {
		t.Fatal("warm hook remained after removal")
	}
}
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

type allowClassifier struct{}

func (allowClassifier) Classify(api.ClassifyRequest) (api.ClassifyResponse, error) {
	return api.ClassifyResponse{Decision: api.Allow, Source: "model"}, nil
}

func TestHookStoresPreviousUserMessagesInTheAudit(t *testing.T) {
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, `{"type":"assistant","message":{"content":"reply"}}`, `{"type":"user","message":{"content":"question `+string(rune('a'+i))+` token sk-abcdefghijklmnopqrstuv"}}`)
	}
	dir := t.TempDir()
	tr := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(tr, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	audit := filepath.Join(dir, "audit")
	for _, tc := range []struct {
		n, want int
		cmds    string
	}{{10, 10, "full"}, {3, 3, "full"}, {0, 0, "full"}, {10, 0, "hash"}} {
		env := hosts.Env{Home: dir, AuditDir: audit, AuditContext: tc.n, AuditCommands: tc.cmds, Getenv: func(string) string { return "" }, Now: time.Now, Stderr: &bytes.Buffer{}}
		_ = os.RemoveAll(audit)
		a := New(env)
		a.Hook(strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"transcript_path":"`+tr+`","session_id":"s1"}`), &bytes.Buffer{}, allowClassifier{})
		es, err := decisionlog.ReadLast(env.AuditFile("claude"), 1)
		if err != nil || len(es) != 1 {
			t.Fatalf("n=%d: no audit line: %v", tc.n, err)
		}
		c := es[0].Context
		if len(c) != tc.want {
			t.Fatalf("n=%d cmds=%s: %d messages, want %d", tc.n, tc.cmds, len(c), tc.want)
		}
		if tc.want == 10 && (!strings.HasPrefix(c[9], "question l ") || !strings.HasPrefix(c[0], "question c ")) {
			t.Fatalf("wrong window: first=%q last=%q", c[0], c[9])
		}
		for _, m := range c {
			if strings.Contains(m, "sk-abc") || strings.Contains(m, "reply") {
				t.Fatalf("secret or assistant text stored: %q", m)
			}
		}
	}
}

func TestObserveAppendsRawRedactedEventsInTheOldFormat(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "data", "events.jsonl")
	env := hosts.Env{Home: dir, EventsFile: events, Getenv: func(string) string { return "" }, Now: func() time.Time { return time.Unix(1791719415, 500_000_000) }, Stderr: &bytes.Buffer{}}
	a := New(env)
	a.Observe(strings.NewReader(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_use_id":"t1","tool_name":"Read","permission_mode":"auto","tool_input":{"file_path":"/x"},"tool_response":{"stdout":"key=sk-abcdefghijklmnopqrstuv and API_TOKEN=hunter2","n":12345678901234567890},"duration_ms":7}`))
	a.Observe(strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_use_id":"t2","tool_name":"Bash","tool_input":{"command":"ls"}}`))
	a.Observe(strings.NewReader(`not json`))
	b, err := os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines (bad input is skipped), got %d: %s", len(lines), b)
	}
	var got struct {
		TS             float64        `json:"ts"`
		Event          string         `json:"event"`
		SessionID      string         `json:"session_id"`
		ToolUseID      string         `json:"tool_use_id"`
		ToolName       string         `json:"tool_name"`
		PermissionMode string         `json:"permission_mode"`
		Raw            map[string]any `json:"raw"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Event != "PostToolUse" || got.SessionID != "s1" || got.ToolUseID != "t1" || got.ToolName != "Read" || got.PermissionMode != "auto" || got.TS != 1791719415.5 || got.Raw["hook_event_name"] != "PostToolUse" {
		t.Fatalf("unexpected line: %s", lines[0])
	}
	if strings.Contains(lines[0], "sk-abc") || strings.Contains(lines[0], "hunter2") || !strings.Contains(lines[0], "12345678901234567890") {
		t.Fatalf("secrets must be redacted and numbers kept exact: %s", lines[0])
	}
	if st, _ := os.Stat(events); st.Mode().Perm() != 0o600 {
		t.Fatalf("events file mode %v", st.Mode().Perm())
	}
	// no events file configured: nothing is written and nothing fails
	New(hosts.Env{Home: dir, Getenv: func(string) string { return "" }, Now: time.Now, Stderr: &bytes.Buffer{}}).Observe(strings.NewReader(`{"hook_event_name":"PostToolUse"}`))
}
