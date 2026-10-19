package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenParity(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../../testdata/golden", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Level       int             `json:"level"`
		Why         []string        `json:"why"`
		Tier        string          `json:"tier"`
		Answer      json.RawMessage `json:"answers"`
		Bare        bool            `json:"bare"`
		Conditional bool            `json:"conditional"`
		Verdict     string          `json:"verdict"`
		Reason      string          `json:"reason"`
		NeedsModel  bool            `json:"needs_model"`
	}
	var rows []row
	if err = json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for i, x := range rows {
		var a *Answers
		if string(x.Answer) != "null" && len(x.Answer) > 0 {
			a = new(Answers)
			if err = json.Unmarshal(x.Answer, a); err != nil {
				t.Fatal(err)
			}
		}
		v, r, n := Decide(x.Level, x.Why, x.Tier, a, x.Bare, x.Conditional)
		if v == x.Verdict && r == x.Reason && n == x.NeedsModel {
			matched++
		} else if matched+1 < len(rows) {
			t.Errorf("pipeline case %d got=(%s,%s,%t) want=(%s,%s,%t)", i, v, r, n, x.Verdict, x.Reason, x.NeedsModel)
		}
	}
	t.Logf("pipeline parity: %d/%d (%.4f%%)", matched, len(rows), 100*float64(matched)/float64(len(rows)))
	if matched != len(rows) {
		t.Fatalf("pipeline parity %d/%d", matched, len(rows))
	}
}
