package decider

import (
	"encoding/json"
	"fmt"
)

// Tokenizer reads the vocabulary portion of a Hugging Face tokenizer.json.
// Model-specific BPE normalization/pre-tokenization is deliberately rejected
// unless supported, rather than silently producing incorrect logits.
type Tokenizer struct {
	vocab map[string]int32
}

// LoadTokenizer loads a tokenizer vocabulary for answer token lookup.
func LoadTokenizer(data []byte) (*Tokenizer, error) {
	var doc struct {
		Model struct {
			Vocab map[string]int32 `json:"vocab"`
		} `json:"model"`
		Added []struct {
			ID      int32  `json:"id"`
			Content string `json:"content"`
		} `json:"added_tokens"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("tokenizer.json: %w", err)
	}
	v := doc.Model.Vocab
	if len(v) == 0 {
		return nil, fmt.Errorf("tokenizer.json has no model vocabulary")
	}
	for _, tok := range doc.Added {
		v[tok.Content] = tok.ID
	}
	return &Tokenizer{vocab: v}, nil
}

// TokenID returns the id for an exact vocabulary token.
func (t *Tokenizer) TokenID(token string) (int32, bool) { id, ok := t.vocab[token]; return id, ok }

// TokenizerJSONAnswerTokens finds the one-token spellings used in answer slots.
// The caller verifies these against the vocabulary before inference.
func TokenizerJSONAnswerTokens(data []byte, labels []string) (map[string]int32, error) {
	t, err := LoadTokenizer(data)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int32, len(labels))
	for _, label := range labels {
		id, ok := t.TokenID(label)
		if !ok {
			return nil, fmt.Errorf("answer token %q is not a single vocabulary token", label)
		}
		out[label] = id
	}
	return out, nil
}
