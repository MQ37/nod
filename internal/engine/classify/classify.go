// Package classify joins deterministic checks, prompt construction and the Decider.
package classify

import (
	"errors"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decider"
	"github.com/MQ37/nod/internal/engine/conversation"
	"github.com/MQ37/nod/internal/engine/pipeline"
	"github.com/MQ37/nod/internal/engine/prompt"
	"github.com/MQ37/nod/internal/engine/rules"
	"github.com/MQ37/nod/internal/engine/tiers"
)

// Classifier is the production implementation of api.Classifier. A missing or
// warming model follows pipeline's fail-safe rules-only branch.
type Classifier struct {
	Decider decider.Decider
	// ModelOnly skips the deterministic tiers and rules: the model judges every call. With no
	// model the answer is ask (there is no rule to fall back on).
	ModelOnly bool
	// Agent marks a server without a local model: host plugins judge with the agent's own model
	// through /v1/review/*, so /v1/classify can only ask.
	Agent bool
}

func (c Classifier) Classify(req api.ClassifyRequest) (api.ClassifyResponse, error) {
	tier := "?"
	lower := req.Tool
	if lower == "Bash" || lower == "bash" || lower == "Shell" || lower == "shell" {
		tier = tiers.Tier(req.Input, req.Cwd)
	} else {
		tier = tiers.FileTool(req.Tool, req.Input, req.Cwd)
	}
	level, why := rules.Evaluate(req.Tool, req.Input, req.Cwd, rules.DefaultOptions())
	if c.ModelOnly {
		tier, level, why = "?", 0, nil
	}
	if tier != "?" && conversation.HasRecentNegation(req.Conversation) {
		tier = "?"
	}
	resp := api.ClassifyResponse{Decision: api.Ask, Source: "rules", Rules: why, Tier: tier, Reason: "model unavailable: rules only"}
	if tier != "?" && level == 0 {
		resp.Decision = api.Allow
		resp.Reason = "tier " + tier
		resp.Source = "tier"
		return resp, nil
	}
	if c.Decider == nil {
		return c.failSafe(resp, level, nil), nil
	}
	contextText, schema, conversationPrompt := makePrompt(req)
	answers, err := c.Decider.Decide(contextText, schema)
	if err != nil {
		return c.failSafe(resp, level, err), nil
	}
	pa, category, ok := decodeAnswers(answers, conversationPrompt)
	if !ok {
		return c.failSafe(resp, level, nil), nil
	}
	return finish(resp, req, level, why, tier, pa, category, "model"), nil
}

// finish runs the severity and consent pipeline on decoded answers and fills the response. source
// names who judged: model (the Decider) or agent (the host's own model).
func finish(resp api.ClassifyResponse, req api.ClassifyRequest, level int, why []string, tier string, pa pipeline.Answers, category, source string) api.ClassifyResponse {
	bare := false
	if req.Conversation != nil {
		_, _, bare = conversation.ShortReplyProposal(req.Conversation)
	}
	conditional := conversation.LastUserConditional(req.Conversation)
	verdict, reason, ran := pipeline.Decide(level, why, tier, &pa, bare, conditional)
	resp.Decision = api.Decision(verdict)
	resp.Reason = reason
	resp.Category = category
	resp.ModelRan = ran
	resp.Source = source
	if level != 0 && source == "model" {
		resp.Source = "rules+model"
	}
	resp.PAllow = &pa.PAllow
	resp.PBlock = &pa.PBlock
	resp.PAsk = ptr(1 - pa.PAllow - pa.PBlock)
	resp.PAuth = &pa.Auth
	resp.PForbid = &pa.Forbid
	resp.PMatch = &pa.Match
	resp.PNamed = &pa.Named
	return resp
}

func (c Classifier) failSafe(r api.ClassifyResponse, level int, cause error) api.ClassifyResponse {
	if level == rules.Block {
		r.Decision = api.Block
	} else {
		r.Decision = api.Ask
	}
	r.Source = "rules"
	if c.ModelOnly {
		r.Source = "none"
	}
	r.ModelRan = false
	switch {
	case c.Agent:
		r.Reason = "no local model (agent review mode); the host plugin judges with the agent's own model"
	case errors.Is(cause, decider.ErrPaused):
		r.Reason = cause.Error()
	case c.ModelOnly:
		r.Reason = "model unavailable"
	default:
		r.Reason = "model unavailable: rules only"
	}
	return r
}
func ptr(v float64) *float64 { return &v }

func makePrompt(req api.ClassifyRequest) (string, decider.Schema, bool) {
	text, spec := prompt.RenderSchema(req.Task, req.Tool, req.Input, req.Cwd, req.Conversation)
	_ = spec // the wire schema is constructed from the same fixed category list below
	conv := req.Conversation != nil
	catKey := prompt.QCategory
	s := decider.Schema{Order: []string{catKey}, Fields: map[string]decider.Field{catKey: {Type: decider.TypeChoice}}}
	catField := s.Fields[catKey]
	for _, c := range prompt.Categories {
		catField.Options = append(catField.Options, c.Description)
	}
	s.Fields[catKey] = catField
	if conv {
		for _, k := range []string{prompt.QAuth, prompt.QForbid, prompt.QMatch, prompt.QNamed} {
			s.Order = append(s.Order, k)
			s.Fields[k] = decider.Field{Type: decider.TypeChoice, Options: []string{"yes", "no"}}
		}
	} else {
		for _, k := range []string{"Could this call cause irreversible damage or leak private data?", "Is this call a normal step towards the user's task?"} {
			s.Order = append(s.Order, k)
			s.Fields[k] = decider.Field{Type: decider.TypeChoice, Options: []string{"yes", "no"}}
		}
	}
	return text, s, conv
}

func decodeAnswers(a map[string]decider.Answer, conv bool) (pipeline.Answers, string, bool) {
	cat, ok := a[prompt.QCategory]
	if !ok || cat.Choice == "" {
		return pipeline.Answers{}, "", false
	}
	var p pipeline.Answers
	category := cat.Choice
	for _, c := range prompt.Categories {
		if c.Description == category {
			switch c.Verdict {
			case "allow":
				p.PAllow += cat.Prob("allow")
			case "block":
				p.PBlock += cat.Prob("block")
			case "ask":
			}
			break
		}
	}
	// Category choices are labels; probability mass is summed by the policy verdict.
	p.PAllow = 0
	p.PBlock = 0
	for _, c := range prompt.Categories {
		switch c.Verdict {
		case "allow":
			p.PAllow += cat.Prob(c.Description)
		case "block":
			p.PBlock += cat.Prob(c.Description)
		}
	}
	if conv {
		p.Auth = yesProb(a, prompt.QAuth)
		p.Forbid = yesProb(a, prompt.QForbid)
		p.Match = yesProb(a, prompt.QMatch)
		p.Named = yesProb(a, prompt.QNamed)
	} else {
		p.Forbid = yesProb(a, "Could this call cause irreversible damage or leak private data?")
		p.Auth = yesProb(a, "Is this call a normal step towards the user's task?")
	}
	return p, category, true
}
func yesProb(a map[string]decider.Answer, key string) float64 {
	x, ok := a[key]
	if !ok {
		return 0
	}
	return x.Prob("yes")
}

// Ensure warming up uses fail-safe rules-only behaviour rather than bubbling
// model errors into the HTTP layer.
