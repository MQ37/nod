package prompt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/engine/conversation"
)

func TestPromptGoldenParity(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../../testdata/golden", "prompts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Task         string     `json:"task"`
		Tool         string     `json:"tool"`
		Input        string     `json:"input"`
		Cwd          string     `json:"cwd"`
		Conversation []api.Turn `json:"conversation"`
		Render       string     `json:"prompt"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for i, x := range rows {
		got, _ := Render(x.Task, x.Tool, x.Input, x.Cwd, x.Conversation)
		if got == x.Render {
			matched++
		} else if matched+1 < len(rows) {
			t.Errorf("prompt row %d mismatch; got=%q want=%q", i, got, x.Render)
		}
	}
	t.Logf("prompt parity: %d/%d (%.4f%%)", matched, len(rows), 100*float64(matched)/float64(len(rows)))
	if matched != len(rows) {
		t.Fatalf("prompt parity %d/%d", matched, len(rows))
	}
}

func TestPromptConstantsGolden(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../../testdata/golden", "prompt_consts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var x struct {
		Cats    [][2]string `json:"cats"`
		PolicyD string      `json:"policy_d"`
	}
	if err = json.Unmarshal(b, &x); err != nil {
		t.Fatal(err)
	}
	if len(x.Cats) != len(Categories) {
		t.Fatalf("categories %d want %d", len(Categories), len(x.Cats))
	}
	for i, c := range x.Cats {
		if len(c) != 2 || c[0] != Categories[i].Description || c[1] != Categories[i].Verdict {
			t.Errorf("category %d differs", i)
		}
	}
	if x.PolicyD != PolicyD {
		t.Error("POLICY_D differs from reference")
	}
}

func TestShortReplyGolden(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../../testdata/golden", "short_reply.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Text  string `json:"text"`
		Short bool   `json:"short"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, x := range rows {
		if conversation.IsShortReply(x.Text) == x.Short {
			matched++
		} else {
			t.Errorf("short reply %q got %t want %t", x.Text, conversation.IsShortReply(x.Text), x.Short)
		}
	}
	t.Logf("short reply parity: %d/%d", matched, len(rows))
	if matched != len(rows) {
		t.Fatalf("short reply parity %d/%d", matched, len(rows))
	}
}
