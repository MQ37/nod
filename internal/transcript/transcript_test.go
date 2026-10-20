package transcript

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestFromLinesFiltersAndPairs(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"content":"Question"}}`,
		`{"type":"user","message":{"content":"yes"}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":"secret"}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"never"},{"type":"text","text":"<system"}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"real"},{"type":"tool_result","content":"hidden"}]}}`,
	}
	got := FromLines(lines)
	if len(got) != 3 || got[0].Role != "assistant" || got[1].Text != "yes" || got[2].Text != "real" {
		t.Fatalf("unexpected turns: %#v", got)
	}
}

func TestFromLinesKeepsLastSixUsers(t *testing.T) {
	var lines []string
	for i := 0; i < 8; i++ {
		a, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]string{"content": "reply"}})
		lines = append(lines, string(a))
		u, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]string{"content": string(rune('a' + i))}})
		lines = append(lines, string(u))
	}
	got := FromLines(lines)
	if userCount(got) != 6 || len(got) != 12 || got[0].Text != "reply" || got[1].Text != "c" || got[11].Text != "h" {
		t.Fatalf("unexpected turns: %#v", got)
	}
}

func TestClipHeadTail(t *testing.T) {
	got := ClipHeadTail(strings.Repeat("x", 400), 300)
	if len([]rune(got)) != 300 || !strings.Contains(got, "[...] ") {
		t.Fatalf("clip length/marker: %d %q", len([]rune(got)), got)
	}
}

func TestUserTextsKeepsUpToNNewestRealUserMessages(t *testing.T) {
	var lines []string
	for i := 0; i < 12; i++ {
		a, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]string{"content": "assistant text must never appear"}})
		lines = append(lines, string(a))
		u, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]string{"content": "msg" + string(rune('a'+i))}})
		lines = append(lines, string(u))
	}
	lines = append(lines,
		`{"type":"user","isMeta":true,"message":{"content":"meta"}}`,
		`{"type":"user","isSidechain":true,"message":{"content":"sidechain"}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"tool output"}]}}`,
		`{"type":"user","message":{"content":"<system-reminder>x"}}`)
	got := userTextsFromLines(lines, 10)
	if len(got) != 10 || got[0] != "msgc" || got[9] != "msgl" {
		t.Fatalf("got %#v", got)
	}
	if few := userTextsFromLines(lines[:6], 10); len(few) != 3 || few[0] != "msga" {
		t.Fatalf("fewer than n must return all: %#v", few)
	}
	if userTextsFromLines(lines, 0) != nil && len(userTextsFromLines(lines, 0)) != 0 {
		t.Fatal("n=0 must return nothing")
	}
	if UserTexts("/nonexistent", 10) != nil {
		t.Fatal("unreadable transcript must give nil")
	}
}

func TestUserTextsReadsBackPastLargeToolOutput(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.jsonl"
	var b strings.Builder
	for i := 0; i < 12; i++ {
		u, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]string{"content": "human " + string(rune('a'+i))}})
		b.WriteString(string(u) + "\n")
		big, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]string{"content": strings.Repeat("tool output ", 5000)}})
		for k := 0; k < 3; k++ {
			b.WriteString(string(big) + "\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	old := userScanChunk
	userScanChunk = 50_000 // smaller than one line, so lines span chunks
	defer func() { userScanChunk = old }()
	got := UserTexts(path, 10)
	if len(got) != 10 || got[0] != "human c" || got[9] != "human l" {
		t.Fatalf("got %#v", got)
	}
	if few := UserTexts(path, 50); len(few) != 12 || few[0] != "human a" {
		t.Fatalf("fewer than n must return all: %#v", few)
	}
	userScanMax = 150_000
	defer func() { userScanMax = 64 << 20 }()
	if capped := UserTexts(path, 10); len(capped) >= 10 {
		t.Fatalf("scan cap must bound the read: %d", len(capped))
	}
}
