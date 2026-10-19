package hosts

import (
	"strings"
	"testing"
)

func TestEnvFileDiffShowsNoNeighbouringSecrets(t *testing.T) {
	before := "A=1\nSERVICE_API_KEY=key-SECRETVALUE\nB=2\n"
	after := before + "# nod begin\nNOD_URL=http://127.0.0.1:11096\n# nod end\n"
	d := UnifiedDiff("/home/u/.hermes/.env", before, after)
	if strings.Contains(d, "SECRETVALUE") || strings.Contains(d, "SERVICE_API_KEY") || strings.Contains(d, "A=1") {
		t.Fatalf("an .env diff must show only the changed lines:\n%s", d)
	}
	if !strings.Contains(d, "+NOD_URL=http://127.0.0.1:11096") {
		t.Fatalf("the change itself must be shown:\n%s", d)
	}
}

func TestOrdinaryFileDiffKeepsContext(t *testing.T) {
	d := UnifiedDiff("/home/u/settings.json", "a\nb\nc\n", "a\nb\nX\nc\n")
	if !strings.Contains(d, " a\n") || !strings.Contains(d, "+X") {
		t.Fatalf("context lines expected:\n%s", d)
	}
}
