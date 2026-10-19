package rules

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGoldenParity(t *testing.T) {
	f, err := os.Open(filepath.Join("../../../testdata/golden", "rules_tiers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type expected struct {
		Level   int
		Reasons []string
	}
	type row struct {
		Tool    string             `json:"tool"`
		Cmd     string             `json:"cmd"`
		Cwd     string             `json:"cwd"`
		Default [2]json.RawMessage `json:"rules_default"`
		AllAsk  [2]json.RawMessage `json:"rules_all_ask"`
	}
	decode := func(raw [2]json.RawMessage) expected {
		var e expected
		if err := json.Unmarshal(raw[0], &e.Level); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw[1], &e.Reasons); err != nil {
			t.Fatal(err)
		}
		if e.Reasons == nil {
			e.Reasons = []string{}
		}
		return e
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1<<20)
	total, defaultMatched, allMatched := 0, 0, 0
	var defaultExamples, allExamples []string
	for s.Scan() {
		total++
		var x row
		if err = json.Unmarshal(s.Bytes(), &x); err != nil {
			t.Fatal(err)
		}
		d := decode(x.Default)
		got, why := Evaluate(x.Tool, x.Cmd, x.Cwd, Options{})
		if got == d.Level && reflect.DeepEqual(why, d.Reasons) {
			defaultMatched++
		} else if len(defaultExamples) < 5 {
			defaultExamples = append(defaultExamples, fmt.Sprintf("row %d tool=%s cmd=%q got=(%d,%q) want=(%d,%q)", total, x.Tool, x.Cmd, got, why, d.Level, d.Reasons))
		}
		a := decode(x.AllAsk)
		opt := DefaultOptions()
		opt.AskIgnore = map[string]bool{}
		got, why = Evaluate(x.Tool, x.Cmd, x.Cwd, opt)
		if got == a.Level && reflect.DeepEqual(why, a.Reasons) {
			allMatched++
		} else if len(allExamples) < 5 {
			allExamples = append(allExamples, fmt.Sprintf("row %d tool=%s cmd=%q got=(%d,%q) want=(%d,%q)", total, x.Tool, x.Cmd, got, why, a.Level, a.Reasons))
		}
	}
	if err = s.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("rules default parity: %d/%d (%.4f%%)", defaultMatched, total, 100*float64(defaultMatched)/float64(total))
	t.Logf("rules all-ASK parity: %d/%d (%.4f%%)", allMatched, total, 100*float64(allMatched)/float64(total))
	if defaultMatched != total || allMatched != total {
		t.Fatalf("rules parity default=%d/%d examples=%v all-ASK=%d/%d examples=%v", defaultMatched, total, defaultExamples, allMatched, total, allExamples)
	}
}
