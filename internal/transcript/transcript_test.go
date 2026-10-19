package transcript

import (
	"encoding/json"
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
