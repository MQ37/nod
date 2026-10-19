package classify

import (
	"errors"
	"testing"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decider"
)

type failingDecider struct{ called bool }

func (d *failingDecider) Decide(string, decider.Schema) (map[string]decider.Answer, error) {
	d.called = true
	return nil, errors.New("test model unavailable")
}

func TestClassifyTierAndRulesFailSafe(t *testing.T) {
	tests := []struct {
		name, tool, input string
		want              api.Decision
	}{
		{"tier allow", "Bash", "git status", api.Allow},
		{"rules block", "Bash", "rm -rf /", api.Block},
		{"unknown asks", "Bash", "some-unknown-command", api.Ask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (Classifier{}).Classify(api.ClassifyRequest{Tool: tt.tool, Input: tt.input, Cwd: "/tmp/project"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision != tt.want {
				t.Fatalf("decision=%s want %s (%+v)", got.Decision, tt.want, got)
			}
		})
	}
}

func TestRecentNegationOverridesTierForDestructiveCall(t *testing.T) {
	d := &failingDecider{}
	req := api.ClassifyRequest{
		Tool: "Bash", Input: "rm -rf node_modules", Cwd: "/tmp/project",
		Conversation: []api.Turn{
			{Role: "user", Text: "Please clean up node_modules."},
			{Role: "assistant", Text: "I propose rm -rf node_modules."},
			{Role: "user", Text: "Yes, delete them. Actually, stop. Do not delete the folder."},
		},
	}
	got, err := (Classifier{Decider: d}).Classify(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != "?" || !d.called {
		t.Fatalf("recent negation did not bypass tier: tier=%q model_called=%v response=%+v", got.Tier, d.called, got)
	}
	if got.Decision == api.Allow {
		t.Fatalf("destructive call was allowed after negation: %+v", got)
	}
}

func TestTierVerdictUsesTierSource(t *testing.T) {
	got, err := (Classifier{}).Classify(api.ClassifyRequest{Tool: "Bash", Input: "git status", Cwd: "/tmp/project"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "tier" || got.Tier == "?" || got.Reason != "tier "+got.Tier {
		t.Fatalf("tier result = source %q, tier %q, reason %q", got.Source, got.Tier, got.Reason)
	}
}

func TestModelOnlySkipsTiersAndRules(t *testing.T) {
	// Without the model nothing is auto-allowed or blocked: even `git status` and `rm -rf /` ask.
	for _, in := range []string{"git status", "rm -rf /"} {
		got, err := (Classifier{ModelOnly: true}).Classify(api.ClassifyRequest{Tool: "Bash", Input: in, Cwd: "/tmp/project"})
		if err != nil || got.Decision != api.Ask || got.Source != "none" || got.ModelRan {
			t.Fatalf("%q: %+v err=%v", in, got, err)
		}
	}
	// With a model, a tier-eligible command still goes to the model.
	d := &failingDecider{}
	if _, err := (Classifier{Decider: d, ModelOnly: true}).Classify(api.ClassifyRequest{Tool: "Bash", Input: "git status", Cwd: "/tmp/project"}); err != nil || !d.called {
		t.Fatalf("model-only must call the decider for a tier command (called=%v err=%v)", d.called, err)
	}
	// Hybrid (default) still allows it deterministically without the model.
	d2 := &failingDecider{}
	got, _ := (Classifier{Decider: d2}).Classify(api.ClassifyRequest{Tool: "Bash", Input: "git status", Cwd: "/tmp/project"})
	if d2.called || got.Decision != api.Allow {
		t.Fatalf("hybrid regression: %+v called=%v", got, d2.called)
	}
}
