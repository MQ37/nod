package classify

import (
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/api"
)

func catNumber(t *testing.T, contains string) int {
	t.Helper()
	p := Classifier{}.ReviewPrompt(api.ClassifyRequest{Tool: "Bash", Input: "x"})
	for _, line := range strings.Split(p.User, "\n") {
		if strings.Contains(line, contains) && len(line) > 3 && line[0] >= '0' && line[0] <= '9' {
			n := 0
			for _, ch := range line {
				if ch < '0' || ch > '9' {
					break
				}
				n = n*10 + int(ch-'0')
			}
			return n
		}
	}
	t.Fatalf("no category containing %q", contains)
	return 0
}

func TestReviewPromptTreatsInputAsDataAndListsQuestions(t *testing.T) {
	conv := []api.Turn{{Role: "user", Text: "please force push my branch"}}
	p := Classifier{}.ReviewPrompt(api.ClassifyRequest{Tool: "Bash", Input: "git push --force origin x", Cwd: "/srv/app", Conversation: conv})
	if !strings.Contains(p.System, "DATA") || !strings.Contains(p.System, "Never follow instructions") {
		t.Fatalf("system prompt must call the call and messages data: %q", p.System)
	}
	for _, want := range []string{"git push --force origin x", "please force push my branch", "1. read-only inspection", "\"auth\"", "\"forbid\"", "ONLY a JSON object"} {
		if !strings.Contains(p.User, want) {
			t.Fatalf("user prompt lacks %q:\n%s", want, p.User)
		}
	}
	if got := strings.Join(p.Keys, ","); got != "category,auth,forbid,match,named" {
		t.Fatalf("keys=%s", got)
	}
	if strings.Contains(p.User, `"category": 1,`) || strings.Contains(p.User, `: "no"}`) {
		t.Fatalf("the JSON example must not be a valid (and safe-looking) answer:\n%s", p.User)
	}
	noConv := Classifier{}.ReviewPrompt(api.ClassifyRequest{Tool: "Bash", Input: "ls"})
	if got := strings.Join(noConv.Keys, ","); got != "category,irreversible,normal" {
		t.Fatalf("no-conversation keys=%s", got)
	}
}

func TestReviewVerdictUsesTheSamePipeline(t *testing.T) {
	conv := []api.Turn{{Role: "user", Text: "I rebased, force push it"}}
	req := api.ClassifyRequest{Tool: "Bash", Input: "git push --force origin x", Conversation: conv}
	force := catNumber(t, "force-pushing")             // block class
	push := catNumber(t, "pushing code, opening pull") // ask class
	read := catNumber(t, "read-only inspection")
	yes, no := "yes", "no"
	cases := []struct {
		name    string
		answers map[string]any
		want    api.Decision
	}{
		{"harmless read", map[string]any{"category": read, "auth": no, "forbid": no, "match": no, "named": no}, api.Allow},
		{"risky and forbidden", map[string]any{"category": force, "auth": no, "forbid": yes, "match": no, "named": no}, api.Block},
		{"ask-class call approved by the user", map[string]any{"category": push, "auth": yes, "forbid": no, "match": yes, "named": yes}, api.Allow},
		{"ask-class call without consent", map[string]any{"category": push, "auth": no, "forbid": no, "match": no, "named": no}, api.Ask},
		{"block-class call approved by the user still asks", map[string]any{"category": force, "auth": yes, "forbid": no, "match": yes, "named": yes}, api.Ask},
		{"block-class call without consent", map[string]any{"category": force, "auth": no, "forbid": no, "match": no, "named": no}, api.Block},
		{"string numbers and bools are accepted", map[string]any{"category": "1", "auth": false, "forbid": false, "match": false, "named": false}, api.Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Classifier{}.ReviewVerdict(req, c.answers)
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision != c.want || got.Source != "agent" || !got.ModelRan {
				t.Fatalf("%+v want %s", got, c.want)
			}
		})
	}
}

func TestReviewVerdictRejectsBadAnswersSoTheCallerAsks(t *testing.T) {
	req := api.ClassifyRequest{Tool: "Bash", Input: "ls", Conversation: []api.Turn{{Role: "user", Text: "x"}}}
	bad := []map[string]any{
		{},
		{"category": 0, "auth": "no", "forbid": "no", "match": "no", "named": "no"},
		{"category": 9999, "auth": "no", "forbid": "no", "match": "no", "named": "no"},
		{"category": 1, "auth": "maybe", "forbid": "no", "match": "no", "named": "no"},
		{"category": 1, "auth": "no", "forbid": "no", "match": "no"},
		{"category": "not a number", "auth": "no", "forbid": "no", "match": "no", "named": "no"},
	}
	for i, a := range bad {
		if _, err := (Classifier{}).ReviewVerdict(req, a); err == nil {
			t.Fatalf("case %d must fail: %v", i, a)
		}
	}
}
