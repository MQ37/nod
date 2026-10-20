// Package transcript rebuilds the conversation context from a Claude Code transcript (JSONL).
// Port of conversation_from_transcript / last_user_task in the Python reference.
//
// Only what the user typed may count as approval: tool results, system/hook messages (text starting
// with "<"), sidechains and meta messages are dropped, and so is the assistant text after the last
// user message. Tool output is never part of the result.
package transcript

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/MQ37/nod/internal/api"
)

const (
	// NUsers is the number of real user turns kept.
	NUsers = 6
	// ClipLen is the per-message clip (rune count), same as CONV_CLIP in guard.py.
	ClipLen = 300
	// TailBytes is how much of the end of the file is read.
	TailBytes = 2_000_000
	taskBytes = 400_000
	// TaskLen is the clip of the "task" (most recent user message).
	TaskLen = 400
)

type line struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// textOf joins the text blocks of a message content (string or block list); tool_result and other blocks are ignored.
func textOf(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}
	if raw[0] != '[' {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ClipHeadTail shortens s to at most n runes keeping the beginning and the end (the end of an assistant
// message is usually the question the user answers), with a marker in the middle.
func ClipHeadTail(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	const mark = " [...] "
	r := []rune(s)
	keep := n - len([]rune(mark))
	head := keep / 2
	tail := keep - head
	return string(r[:head]) + mark + string(r[len(r)-tail:])
}

// ClipHead keeps the first n runes.
func ClipHead(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func tailLines(path string, size int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	total := st.Size()
	start := int64(0)
	if total > size {
		start = total - size
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(bytes.ToValidUTF8(b, []byte("�"))), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is cut in the middle
	}
	return lines, nil
}

// Parse reads the end of the transcript at path and returns the conversation turns, oldest first.
// An error means the transcript is unreadable (callers then send no conversation).
func Parse(path string) ([]api.Turn, error) {
	lines, err := tailLines(path, TailBytes)
	if err != nil {
		return nil, err
	}
	return FromLines(lines), nil
}

// FromLines is Parse over already split JSONL lines.
func FromLines(lines []string) []api.Turn {
	var msgs []api.Turn
	assistant := ""
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var d line
		if json.Unmarshal([]byte(ln), &d) != nil || d.IsSidechain || d.IsMeta {
			continue
		}
		var content json.RawMessage
		if d.Message != nil {
			content = d.Message.Content
		}
		switch d.Type {
		case "assistant":
			if t := textOf(content); t != "" {
				assistant = t
			}
		case "user":
			u := textOf(content)
			if u == "" || strings.HasPrefix(u, "<") {
				continue
			}
			if assistant != "" {
				msgs = append(msgs, api.Turn{Role: "assistant", Text: ClipHeadTail(assistant, ClipLen)})
				assistant = ""
			}
			msgs = append(msgs, api.Turn{Role: "user", Text: ClipHead(u, ClipLen)})
		}
	}
	users := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			users++
			if users == NUsers {
				// Keep the assistant message immediately before this oldest retained
				// user; it is the context that user was replying to.
				if i > 0 && msgs[i-1].Role == "assistant" {
					i--
				}
				msgs = msgs[i:]
				break
			}
		}
	}
	// Keep the invariant explicit even if a transcript's role order differs from
	// the usual assistant-then-user sequence.
	for userCount(msgs) > NUsers {
		firstUser := 0
		for firstUser < len(msgs) && msgs[firstUser].Role != "user" {
			firstUser++
		}
		if firstUser == len(msgs) {
			break
		}
		msgs = msgs[firstUser+1:]
	}
	return msgs
}

func userCount(msgs []api.Turn) int {
	n := 0
	for _, msg := range msgs {
		if msg.Role == "user" {
			n++
		}
	}
	return n
}

// userScanChunk and userScanMax bound how far back UserTexts reads: a session with verbose tool output
// can have its last human messages far from the end of a multi-hundred-megabyte transcript.
var (
	userScanChunk int64 = 1 << 20
	userScanMax   int64 = 64 << 20
)

// UserTexts returns up to n of the most recent real user messages of the session at path, oldest first,
// each clipped to ClipLen. It reads the file backwards in chunks until it has n messages or has read
// userScanMax bytes, so it returns fewer when the session has fewer; an unreadable transcript gives nil.
func UserTexts(path string, n int) []string {
	if n <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	var newest []string // newest first
	var rest []byte     // the incomplete first line of the chunk read before this one
	off, scanned := st.Size(), int64(0)
	for off > 0 && len(newest) < n && scanned < userScanMax {
		size := userScanChunk
		if size > off {
			size = off
		}
		off -= size
		scanned += size
		buf := make([]byte, size)
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			return reverse(newest)
		}
		buf = append(buf, rest...)
		lines := bytes.Split(buf, []byte{'\n'})
		if off > 0 { // the first line may be cut: keep it for the next, older chunk
			rest, lines = lines[0], lines[1:]
		} else {
			rest = nil
		}
		for k := len(lines) - 1; k >= 0 && len(newest) < n; k-- {
			if !bytes.Contains(lines[k], []byte(`"user"`)) {
				continue
			}
			if got := userTextsFromLines([]string{string(lines[k])}, 1); len(got) == 1 {
				newest = append(newest, got[0])
			}
		}
	}
	return reverse(newest)
}

func reverse(s []string) []string {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

func userTextsFromLines(lines []string, n int) []string {
	var out []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var d line
		if json.Unmarshal([]byte(ln), &d) != nil || d.IsSidechain || d.IsMeta || d.Type != "user" || d.Message == nil {
			continue
		}
		u := textOf(d.Message.Content)
		if u == "" || strings.HasPrefix(u, "<") {
			continue
		}
		out = append(out, ClipHead(u, ClipLen))
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// LastUserTask is the most recent real user message of the session (clipped), or "" when unavailable.
func LastUserTask(path string) string {
	lines, err := tailLines(path, taskBytes)
	if err != nil {
		return ""
	}
	for i := len(lines) - 1; i >= 0; i-- {
		var d line
		if json.Unmarshal([]byte(strings.TrimSpace(lines[i])), &d) != nil || d.Type != "user" || d.IsSidechain || d.IsMeta || d.Message == nil {
			continue
		}
		if t := textOf(d.Message.Content); t != "" && !strings.HasPrefix(t, "<") {
			return ClipHead(t, TaskLen)
		}
	}
	return ""
}
