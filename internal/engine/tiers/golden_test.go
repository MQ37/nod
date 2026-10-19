package tiers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenParity(t *testing.T) {
	f, err := os.Open(filepath.Join("../../../testdata/golden", "rules_tiers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type row struct {
		Tool string `json:"tool"`
		Cmd  string `json:"cmd"`
		Cwd  string `json:"cwd"`
		Tier string `json:"tier"`
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1<<20)
	total, matched := 0, 0
	for s.Scan() {
		total++
		var x row
		if err = json.Unmarshal(s.Bytes(), &x); err != nil {
			t.Fatal(err)
		}
		got := Tier(x.Cmd, x.Cwd)
		if x.Tool != "Bash" {
			got = FileTool(x.Tool, x.Cmd, x.Cwd)
		}
		if got == x.Tier {
			matched++
		} else if matched+1 < total {
			t.Errorf("tier mismatch row %d tool=%s cmd=%q cwd=%q got=%q want=%q", total, x.Tool, x.Cmd, x.Cwd, got, x.Tier)
		}
	}
	if err = s.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("tiers parity: %d/%d (%.4f%%)", matched, total, 100*float64(matched)/float64(total))
	if matched != total {
		t.Fatalf("tier parity %d/%d", matched, total)
	}
}
