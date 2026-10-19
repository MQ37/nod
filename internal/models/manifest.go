package models

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed manifest.json
var manifestJSON []byte

// Tier is one installable model.
type Tier struct {
	ID         string `json:"id"`
	Source     string `json:"source"` // hf-gguf | hf-convert
	Repo       string `json:"repo"`
	GGUF       string `json:"gguf"`
	OutType    string `json:"outtype,omitempty"`
	GGUFSHA256 string `json:"gguf_sha256,omitempty"`
	Files      []File `json:"files"`
}

type manifestFile struct {
	Tiers []Tier `json:"tiers"`
}

// Manifest is the embedded pin.
func Manifest() ([]Tier, error) {
	var m manifestFile
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, err
	}
	if len(m.Tiers) == 0 {
		return nil, fmt.Errorf("models: empty manifest")
	}
	return m.Tiers, nil
}

// Lookup finds a tier id.
func Lookup(id string) (Tier, error) {
	all, err := Manifest()
	if err != nil {
		return Tier{}, err
	}
	for _, t := range all {
		if t.ID == id {
			return t, nil
		}
	}
	return Tier{}, fmt.Errorf("unknown model %q (have fast, lite, full, full-q4)", id)
}

// SourceFiles is the download list. full-q4 reuses the full tier's shards.
func (t Tier) SourceFiles() ([]File, error) {
	if len(t.Files) > 0 {
		return t.Files, nil
	}
	if t.Source != "hf-convert" {
		return nil, fmt.Errorf("%s: no files in the manifest", t.ID)
	}
	all, err := Manifest()
	if err != nil {
		return nil, err
	}
	for _, o := range all {
		if o.Repo == t.Repo && len(o.Files) > 0 {
			return o.Files, nil
		}
	}
	return nil, fmt.Errorf("%s: no source files", t.ID)
}

// GGUFFile is the GGUF pin. hf-convert stores the digest in GGUFSHA256.
func (t Tier) GGUFFile() File {
	if t.Source == "hf-gguf" {
		for _, f := range t.Files {
			if f.Path == t.GGUF {
				return f
			}
		}
	}
	return File{Path: t.GGUF, SHA256: t.GGUFSHA256}
}

// TierPinned reports whether the tier's GGUF has a pinned sha256, so an install can verify it
// without --allow-unpinned.
func TierPinned(id string) bool {
	t, err := Lookup(id)
	return err == nil && t.GGUFFile().Pinned()
}
