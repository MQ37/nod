package conversation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MQ37/nod/internal/api"
)

func TestConversationGoldenParity(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../../testdata/golden", "prompts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Conversation []api.Turn `json:"conversation"`
		Render       string     `json:"prompt"`
		Formatted    string     `json:"formatted"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for i, x := range rows {
		got := Format(x.Conversation)
		if got == x.Formatted {
			matched++
		} else if matched+1 < len(rows) {
			t.Errorf("conversation row %d got=%q want=%q", i, got, x.Formatted)
		}
	}
	t.Logf("conversation parity: %d/%d", matched, len(rows))
	if matched != len(rows) {
		t.Fatalf("conversation parity %d/%d", matched, len(rows))
	}
}
