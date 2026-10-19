// Package conversation formats the bounded D4 approval context.
package conversation

import (
	"regexp"
	"strings"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/engine/pycompat"
)

const MaxUsers, Clip = 6, 300

var wordsRE = regexp.MustCompile(`[a-z0-9']+`)
var shortWords = wordSet("yes y yep yeah yup ya yea ok okay k kk sure fine alright right correct please pls plz go ahead do it that this and now just then proceed approved approve confirm confirmed lgtm sgtm sounds good great perfect thanks thank you ty absolutely definitely of course for cool lets let's run thing")
var coreWords = wordSet("yes y yep yeah yup ya yea ok okay k kk sure go proceed approved approve confirm confirmed lgtm sgtm do run")

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func IsShortReply(text string) bool {
	text = strings.ToLower(strings.ReplaceAll(text, "’", "'"))
	w := wordsRE.FindAllString(text, -1)
	if len(w) == 0 || len(w) > 6 {
		return false
	}
	core := false
	for _, x := range w {
		if !shortWords[x] {
			return false
		}
		core = core || coreWords[x]
	}
	return core
}

func ShortReplyProposal(turns []api.Turn) (string, string, bool) {
	var m []api.Turn
	for _, t := range turns {
		if t.Role == "user" || t.Role == "assistant" {
			m = append(m, t)
		}
	}
	for i := len(m) - 1; i >= 0; i-- {
		if m[i].Role == "user" {
			if i > 0 && m[i-1].Role == "assistant" && IsShortReply(m[i].Text) {
				return m[i-1].Text, m[i].Text, true
			}
			return "", "", false
		}
	}
	return "", "", false
}

func Format(turns []api.Turn) string {
	var m []api.Turn
	for _, t := range turns {
		if t.Role == "user" || t.Role == "assistant" {
			m = append(m, t)
		}
	}
	users := []int{}
	for i, t := range m {
		if t.Role == "user" {
			users = append(users, i)
		}
	}
	start := 0
	if len(users) > MaxUsers {
		start = users[len(users)-MaxUsers]
	} else if len(users) > 0 {
		start = users[0]
	}
	var lines []string
	for j := start; j < len(m); j++ {
		t := m[j]
		if t.Role == "user" {
			lines = append(lines, "user: "+pycompat.Prefix(t.Text, Clip))
		} else if j+1 < len(m) && m[j+1].Role == "user" {
			lines = append(lines, "assistant (the user's next message replies to this): "+pycompat.Prefix(t.Text, Clip))
		}
	}
	return strings.Join(lines, "\n")
}
