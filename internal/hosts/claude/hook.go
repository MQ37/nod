package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decisionlog"
	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/transcript"
)

const maxEvent = 16 << 20

type event struct {
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	Cwd            string          `json:"cwd"`
	TranscriptPath string          `json:"transcript_path"`
	SessionID      string          `json:"session_id"`
	ToolUseID      string          `json:"tool_use_id"`
	PermissionMode string          `json:"permission_mode"`
}

// Answer is the PreToolUse answer in Claude Code's wire format.
type Answer struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func hookDecision(d api.Decision) string {
	switch d {
	case api.Allow:
		return "allow"
	case api.Block:
		return "deny"
	}
	return "ask"
}

// Hook is the PreToolUse hook. Shadow mode prints nothing (Claude Code's own permission flow stays in
// charge) and logs the verdict; enforce mode prints the answer. It always returns 0 so that no failure
// can be read as a non-blocking error that lets the call through.
func (a *Adapter) Hook(stdin io.Reader, stdout io.Writer, cl api.Classifier) (code int) {
	env := a.Env
	mode := env.CurrentMode()
	start := time.Now()
	rec := hosts.Verdict{Host: Name, Mode: mode}
	silent := false
	d, why := hookDecision(env.FailDecision()), "nod failed internally; falling back to the normal prompt"
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(env.Stderr, "nod: internal error: %v\n", r)
			rec.Error = "panic"
			d, why = hookDecision(env.FailDecision()), "nod failed internally"
		}
		if silent {
			return
		}
		rec.Ms = int(time.Since(start).Milliseconds())
		rec.Answer = "none"
		if mode == hosts.ModeEnforce {
			rec.Answer = d
		}
		env.LogVerdict(rec)
		if mode == hosts.ModeEnforce {
			var ans Answer
			ans.HookSpecificOutput.HookEventName = "PreToolUse"
			ans.HookSpecificOutput.PermissionDecision = d
			ans.HookSpecificOutput.PermissionDecisionReason = why
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(ans)
			_, _ = stdout.Write(buf.Bytes())
		}
		code = 0
	}()

	raw, err := io.ReadAll(io.LimitReader(stdin, maxEvent))
	var ev event
	if err == nil {
		err = json.Unmarshal(raw, &ev)
	}

	if err != nil {
		rec.Error = "bad hook input"
		fmt.Fprintf(env.Stderr, "nod: cannot read the hook event: %v\n", err)
		why = "nod could not read the hook event; falling back to the normal prompt"
		return
	}
	// SessionStart warm mode is a notification hook: it asks the server to begin
	// loading asynchronously and emits no PreToolUse decision on stdout.
	if ev.HookEventName == "SessionStart" {
		if h, ok := cl.(interface{ HealthWarm(bool) (string, error) }); ok {
			if _, err := h.HealthWarm(true); err != nil {
				fmt.Fprintf(env.Stderr, "nod: model warm request failed: %v\n", err)
			}
		} else {
			fmt.Fprintln(env.Stderr, "nod: classifier does not support model warm requests")
		}
		return 0
	}
	rec.Tool = ev.ToolName
	if ev.HookEventName != "" && ev.HookEventName != "PreToolUse" {
		silent = true // not a PreToolUse event: say nothing, log nothing
		return
	}
	if cl == nil {
		rec.Error = "no classifier"
		why = "nod has no classifier; falling back to the normal prompt"
		return
	}
	req := api.ClassifyRequest{Tool: ev.ToolName, Input: ToolText(ev.ToolName, ev.ToolInput), Cwd: ev.Cwd}
	rec.Command, rec.Session, rec.Cwd = req.Input, ev.SessionID, ev.Cwd
	if ev.TranscriptPath != "" && env.AuditContext > 0 {
		rec.Context = transcript.UserTexts(ev.TranscriptPath, env.AuditContext)
	}
	if ev.TranscriptPath != "" {
		req.Task = transcript.LastUserTask(ev.TranscriptPath)
		if conv, err := transcript.Parse(ev.TranscriptPath); err == nil {
			req.Conversation = conv
		}
	}
	resp, err := cl.Classify(req)
	if err != nil {
		rec.Error = errKind(err)
		fmt.Fprintf(env.Stderr, "nod: classifier unavailable: %v\n", err)
		why = fmt.Sprintf("nod unavailable (%s); falling back to the normal prompt", rec.Error)
		return
	}
	rec.Decision, rec.Source, rec.Rules = string(resp.Decision), resp.Source, resp.Rules
	rec.Category, rec.Reason = resp.Category, resp.Reason
	d = hookDecision(resp.Decision)
	why = reason(resp)
	return
}

func errKind(err error) string {
	s := err.Error()
	if len(s) > 80 {
		s = s[:80]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, s)
}

func reason(r api.ClassifyResponse) string {
	extra := ""
	if len(r.Rules) > 0 {
		extra = "; " + strings.Join(r.Rules, "; ")
	} else {
		var probabilities []string
		if r.PAllow != nil {
			probabilities = append(probabilities, "p_allow="+pyFloat(r.PAllow))
		}
		if r.PBlock != nil {
			probabilities = append(probabilities, "p_block="+pyFloat(r.PBlock))
		}
		if len(probabilities) > 0 {
			extra = "; " + strings.Join(probabilities, " ")
		}
	}
	return fmt.Sprintf("nod: %s (%s)%s", r.Decision, r.Source, extra)
}

func pyFloat(f *float64) string {
	if f == nil {
		return "None"
	}
	return strconv.FormatFloat(*f, 'g', -1, 64)
}

// Observe records one hook event (any event) as a raw line in the events file, in the format of the old
// `toolguard log`: {ts, event, session_id, tool_use_id, tool_name, permission_mode, raw}. Every string in
// the event is redacted first (private keys, Authorization/Bearer values, token prefixes, *KEY/*TOKEN/*SECRET
// values). It classifies nothing and answers nothing.
func (a *Adapter) Observe(stdin io.Reader) int {
	defer func() { _ = recover() }()
	raw, _ := io.ReadAll(io.LimitReader(stdin, maxEvent))
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var ev map[string]any
	if dec.Decode(&ev) != nil || ev == nil {
		return 0
	}
	now := a.Env.Now()
	line := map[string]any{
		"ts":              float64(now.UnixNano()) / 1e9,
		"event":           ev["hook_event_name"],
		"session_id":      ev["session_id"],
		"tool_use_id":     ev["tool_use_id"],
		"tool_name":       ev["tool_name"],
		"permission_mode": ev["permission_mode"],
		"raw":             redactTree(ev),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(line) != nil {
		return 0
	}
	a.Env.AppendEvent(bytes.TrimRight(buf.Bytes(), "\n"))
	return 0
}

// redactTree redacts every string value (and key-free structure is kept) of a decoded JSON value.
func redactTree(v any) any {
	switch x := v.(type) {
	case string:
		return decisionlog.Redact(x)
	case []any:
		for i := range x {
			x[i] = redactTree(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = redactTree(x[k])
		}
		return x
	}
	return v
}

// ---- tool_input flattening (port of tool_text) ----

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// pyStr is str(value) of a decoded JSON value, Python style.
func pyStr(raw json.RawMessage, present bool) string {
	if !present {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	switch string(bytes.TrimSpace(raw)) {
	case "null":
		return "None"
	case "true":
		return "True"
	case "false":
		return "False"
	}
	return string(bytes.TrimSpace(raw))
}

// pyRepr approximates Python's repr(str).
func pyRepr(s string) string {
	q := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		q = '"'
	}
	var b strings.Builder
	b.WriteByte(q)
	for _, r := range s {
		switch {
		case r == rune(q) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(q)
	return b.String()
}

// pySpaced re-spaces compact JSON like Python's json.dumps (", " and ": ") keeping key order.
func pySpaced(raw []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	in, out := b.Bytes(), strings.Builder{}
	str := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		out.WriteByte(c)
		if str {
			if c == '\\' && i+1 < len(in) {
				i++
				out.WriteByte(in[i])
			} else if c == '"' {
				str = false
			}
			continue
		}
		switch c {
		case '"':
			str = true
		case ',', ':':
			out.WriteByte(' ')
		}
	}
	return out.String()
}

// ToolText flattens a Claude Code tool_input into the text the guard inspects.
func ToolText(name string, input json.RawMessage) string {
	var ti map[string]json.RawMessage
	in := bytes.TrimSpace(input)
	if len(in) == 0 || in[0] != '{' || json.Unmarshal(in, &ti) != nil {
		if len(in) == 0 {
			in = []byte("null")
		}
		return trunc(pySpaced(in), 32768)
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := ti[k]; ok {
				return pyStr(v, true)
			}
		}
		return ""
	}
	switch n := strings.ToLower(name); n {
	case "bash":
		return get("command")
	case "read", "notebookread":
		return get("file_path", "notebook_path")
	case "write":
		c := get("content")
		return fmt.Sprintf("%s (write %d chars: %s)", get("file_path", "notebook_path"), utf8.RuneCountInString(c), pyRepr(trunc(c, 300)))
	case "edit":
		return fmt.Sprintf("%s: replace %s with %s", get("file_path", "notebook_path"), pyRepr(trunc(get("old_string"), 200)), pyRepr(trunc(get("new_string"), 300)))
	case "multiedit", "notebookedit":
		return get("file_path", "notebook_path") + " (multiple edits)"
	case "grep", "glob":
		return fmt.Sprintf("pattern: %s path: %s", get("pattern"), get("path"))
	case "webfetch", "websearch":
		return get("url", "query")
	}
	return trunc(pySpaced(in), 32768)
}
