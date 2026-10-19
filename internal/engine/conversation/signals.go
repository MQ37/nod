package conversation

import (
	"github.com/MQ37/nod/internal/api"
	"regexp"
	"strings"
)

var negationRE = regexp.MustCompile(`(?i)\b(don'?t|do not|dont|never|stop|wait|hold off|not yet|no need|without)\b`)
var conditionalRE = regexp.MustCompile(`(?i)\b(if|once|unless|when|after|provided|as long as|assuming)\b`)

func IsConditional(text string) bool { return conditionalRE.MatchString(text) }

func HasRecentNegation(turns []api.Turn) bool {
	var u []string
	for _, t := range turns {
		if t.Role == "user" {
			u = append(u, t.Text)
		}
	}
	start := len(u) - 3
	if start < 0 {
		start = 0
	}
	for _, s := range u[start:] {
		if negationRE.MatchString(s) {
			return true
		}
	}
	return false
}

func LastUserConditional(turns []api.Turn) bool {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" {
			return conditionalRE.MatchString(strings.ToLower(turns[i].Text))
		}
	}
	return false
}
