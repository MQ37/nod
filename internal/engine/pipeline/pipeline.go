// Package pipeline implements the deterministic severity and consent matrix.
package pipeline

import "strings"

// Answers contains the normalized outputs of the Decider questions.
type Answers struct {
	PAllow float64 `json:"p_allow"`
	PBlock float64 `json:"p_block"`
	Auth   float64 `json:"auth"`
	Forbid float64 `json:"forbid"`
	Match  float64 `json:"match"`
	Named  float64 `json:"named"`
}

// Thresholds are the D6 policy cutoffs.
type Thresholds struct {
	Allow, BlockCap, BlockClass, Auth, Approve, ClearMatch, Forbid, Match, AllowMismatch float64
}

var DefaultThresholds = Thresholds{.6, .3, .6, .6, .75, .8, .5, .5, .9}

var t1Prefixes = []string{"writes to a raw device", "formats or wipes a disk", "fork bomb", "overwrites a raw device", "destructive SQL", "DELETE without WHERE", "mass Kubernetes delete", "infrastructure destroy", "cloud resource deletion", "deletes remote data", "git clean -fdx", "root-level destructive", "erases traces", "disables a security control", "recursive delete", "rm --no-preserve-root", "sweeps the filesystem", "cloud metadata", "sends the environment out", "decodes a payload", "reverse-shell", "netcat with -e", "socat exec", "script wrapper", "touches system credential files", "system path", "credential or key path"}

func NeedsModel(level int, tier string) bool { return level != 0 || tier == "?" }

func ModelVerdict(a Answers, bare bool, th Thresholds) string {
	if a.Forbid >= th.Forbid {
		return "block"
	}
	mis := bare && a.Match < th.Match
	allow := th.Allow
	if mis && th.AllowMismatch > allow {
		allow = th.AllowMismatch
	}
	if a.PAllow >= allow && a.PBlock <= th.BlockCap {
		return "allow"
	}
	consent := a.Auth
	if mis {
		consent = a.Named
	}
	if consent >= th.Auth {
		if a.PBlock >= th.BlockClass {
			return "ask"
		}
		return "allow"
	}
	if a.PBlock >= th.BlockClass {
		return "block"
	}
	return "ask"
}

func approved(a Answers, bare bool, th Thresholds) bool {
	consent := a.Auth
	if bare && a.Match < th.Match {
		consent = a.Named
	}
	return a.Forbid < th.Forbid && consent >= th.Approve
}

func isT1(why []string) bool {
	for _, r := range why {
		for _, p := range t1Prefixes {
			if strings.HasPrefix(r, p) {
				return true
			}
		}
	}
	return false
}

// Decide returns verdict, reason, and whether the model was used.
// A nil answer means the model failed; this path is fail-safe.
func Decide(level int, why []string, tier string, answer *Answers, bare, conditional bool) (string, string, bool) {
	return DecideWithThresholds(level, why, tier, answer, bare, conditional, DefaultThresholds)
}

func DecideWithThresholds(level int, why []string, tier string, answer *Answers, bare, conditional bool, th Thresholds) (string, string, bool) {
	if level == 0 && tier != "?" {
		return "allow", "tier " + tier, false
	}
	if answer == nil {
		if level == 2 {
			return "block", "model unavailable: rules only", false
		}
		return "ask", "model unavailable: rules only", false
	}
	a := *answer
	mv := ModelVerdict(a, bare, th)
	close := !bare || a.Match >= th.ClearMatch
	whyText := strings.Join(why[:min(2, len(why))], "; ")
	if level == 2 {
		if approved(a, bare, th) && close && !conditional {
			if isT1(why) {
				return "ask", "approved but irreversible: " + whyText, true
			}
			return "allow", "approved: " + whyText, true
		}
		if mv == "block" || !conditional {
			return "block", whyText, true
		}
		return "ask", whyText, true
	}
	if level == 1 {
		if approved(a, bare, th) && mv == "allow" && close && !conditional && !isT1(why) {
			return "allow", "approved: " + whyText, true
		}
		if mv == "block" {
			return "block", whyText, true
		}
		return "ask", whyText, true
	}
	return mv, "model", true
}
