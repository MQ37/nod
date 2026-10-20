// Package api holds the wire types shared by the server, the hook adapters and the CLI.
// They mirror the Python reference server (server.py, guard.py): POST /v1/classify.
package api

// Decision is the final verdict of the gate.
type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Block Decision = "block"
)

// ExitCode maps a decision to the `nod check` exit status.
func (d Decision) ExitCode() int {
	switch d {
	case Allow:
		return 0
	case Ask:
		return 1
	case Block:
		return 2
	}
	return 3
}

// Turn is one conversation message. Only real user turns and the assistant message each user turn replies to are ever sent; never tool output.
type Turn struct {
	Role string `json:"role"` // "user" | "assistant"
	Text string `json:"text"`
}

// ClassifyRequest is the body of POST /v1/classify.
type ClassifyRequest struct {
	Tool         string `json:"tool"`
	Input        string `json:"input"`
	Task         string `json:"task,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Conversation []Turn `json:"conversation,omitempty"`
	// NoPrompt marks a host that cannot show an approval prompt (an "ask" there only fails the call).
	// The response then carries user_approved when the review found the user's approval of this exact action.
	NoPrompt bool `json:"no_prompt,omitempty"`
}

// ClassifyResponse is the answer of POST /v1/classify (fields omitted when the model did not run).
type ClassifyResponse struct {
	Decision Decision `json:"decision"`
	Source   string   `json:"source"` // tier | rules | model | rules+model | none
	Rules    []string `json:"rules"`
	Tier     string   `json:"tier,omitempty"` // T0 | T0b | ?
	Reason   string   `json:"reason,omitempty"`
	ModelRan bool     `json:"model_ran"`
	PAllow   *float64 `json:"p_allow,omitempty"`
	PAsk     *float64 `json:"p_ask,omitempty"`
	PBlock   *float64 `json:"p_block,omitempty"`
	PAuth    *float64 `json:"p_auth,omitempty"`
	PForbid  *float64 `json:"p_forbid,omitempty"`
	PMatch   *float64 `json:"p_match,omitempty"`
	PNamed   *float64 `json:"p_named,omitempty"`
	Category string   `json:"category,omitempty"`
	// UserApproved is set only for a NoPrompt request whose decision is ask because of the action's
	// severity, while the user had approved it in plain words. The decision itself stays ask.
	UserApproved bool `json:"user_approved,omitempty"`
	Ms           int  `json:"ms"`
	// Via names the server that answered when a fallback chain is in use: remote or local.
	Via   string `json:"via,omitempty"`
	Error string `json:"error,omitempty"`
}

// ReviewPrompt is what a host plugin sends to the agent's own model when nod runs with --model agent.
// The plugin asks the model System and User, then sends the answers to /v1/review/verdict.
type ReviewPrompt struct {
	System string `json:"system"`
	User   string `json:"user"`
	// Keys lists the answer fields the model must return, in order, as one JSON object.
	Keys []string `json:"keys"`
}

// ReviewVerdictRequest carries the call and the model's answers (field name to a number or yes/no).
type ReviewVerdictRequest struct {
	Request ClassifyRequest `json:"request"`
	Answers map[string]any  `json:"answers"`
}

// Reviewer is implemented by classifiers that can build a review prompt and turn a chat model's
// answers into a verdict.
type Reviewer interface {
	ReviewPrompt(req ClassifyRequest) ReviewPrompt
	ReviewVerdict(req ClassifyRequest, answers map[string]any) (ClassifyResponse, error)
}

// Classifier is what the hook adapters call: an HTTP client to a server, or the in-process engine.
type Classifier interface {
	Classify(req ClassifyRequest) (ClassifyResponse, error)
}
