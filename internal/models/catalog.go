package models

import "fmt"

// hints are the sizes and memory a tier needs. They are guidance for the person or agent
// choosing a tier, not something Nod enforces.
var hints = map[string]struct {
	GGUFGB, MinRAMGB float64
	NeedsGPU         bool
	Note             string
}{
	"fast":    {GGUFGB: 2.7, MinRAMGB: 8, Note: "Decider-4B, Q4_K_M. Smallest; fine for a laptop backup."},
	"lite":    {GGUFGB: 12.7, MinRAMGB: 24, Note: "Decider-12B (Gemma-4-12B-it base), Q8_0. Fits next to another large model."},
	"full":    {GGUFGB: 36.9, MinRAMGB: 64, NeedsGPU: true, Note: "Decider-35B-A3B, Q8_0. Needs a GGUF metadata patch after conversion."},
	"full-q4": {Note: "Decider-35B-A3B, Q4_K_M. Measured as lossy; not recommended."},
}

// URL is where a file of the tier's Hugging Face repository is served.
func (t Tier) URL(path string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", t.Repo, path)
}

// FileInfo is a file with its download URL, for `nod models list`.
type FileInfo struct {
	File
	URL string `json:"url"`
}

// Info describes one tier for people and agents: what to fetch, what it costs, how to turn it
// into a GGUF when it is not published as one. Nod itself downloads and converts nothing.
type Info struct {
	ID          string     `json:"id"`
	Repo        string     `json:"repo"`
	Source      string     `json:"source"` // hf-gguf: download the GGUF; hf-convert: download safetensors and convert
	GGUF        string     `json:"gguf"`
	GGUFSHA256  string     `json:"gguf_sha256,omitempty"` // empty or unpinned for converted models: hashes differ per llama.cpp build
	GGUFGB      float64    `json:"gguf_gb,omitempty"`
	MinRAMGB    float64    `json:"min_ram_gb,omitempty"`
	NeedsGPU    bool       `json:"needs_gpu,omitempty"`
	Recommended bool       `json:"recommended,omitempty"`
	Note        string     `json:"note,omitempty"`
	Files       []FileInfo `json:"files"`
	Convert     []Step     `json:"convert,omitempty"`
	ChatLayout  bool       `json:"needs_chat_sidecar,omitempty"`
}

// Describe builds the Info for a tier. src and dst fill the conversion command lines.
func Describe(t Tier, src, dst string) (Info, error) {
	files, err := t.SourceFiles()
	if err != nil {
		return Info{}, err
	}
	h := hints[t.ID]
	in := Info{ID: t.ID, Repo: t.Repo, Source: t.Source, GGUF: t.GGUF, GGUFGB: h.GGUFGB, MinRAMGB: h.MinRAMGB, NeedsGPU: h.NeedsGPU, Note: h.Note, ChatLayout: t.ID == "lite"}
	if t.GGUFFile().Pinned() {
		in.GGUFSHA256 = t.GGUFFile().SHA256
	}
	for _, f := range files {
		in.Files = append(in.Files, FileInfo{File: f, URL: t.URL(f.Path)})
	}
	if t.Source == "hf-convert" {
		if in.Convert, err = ConvertSteps(t, src, dst); err != nil {
			return Info{}, err
		}
	}
	return in, nil
}

// Step is one command of a conversion.
type Step struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// ConvertSteps are the commands that turn a downloaded safetensors directory into a GGUF with
// llama.cpp's converter. The converter comes from the llama.cpp tree the user trusts, never from
// the model repository.
func ConvertSteps(t Tier, src, dst string) ([]Step, error) {
	if t.Source != "hf-convert" {
		return nil, fmt.Errorf("%s: not an hf-convert tier", t.ID)
	}
	if t.OutType == "" {
		return nil, fmt.Errorf("%s: outtype is empty", t.ID)
	}
	steps := []Step{{Name: "convert", Detail: fmt.Sprintf("python3 convert_hf_to_gguf.py %s --outtype %s --outfile %s", src, t.OutType, dst)}}
	if t.Repo == "Mapika/decider-35b-a3b" {
		steps = append(steps,
			Step{Name: "patch-block-count", Detail: fmt.Sprintf("python3 gguf_set_metadata.py --force %s qwen35moe.block_count 40", dst)},
			Step{Name: "patch-nextn", Detail: fmt.Sprintf("python3 gguf_set_metadata.py --force %s qwen35moe.nextn_predict_layers 0", dst)})
	}
	return steps, nil
}

// Recommend picks the largest tier the machine fits: full needs >= 64 GB RAM and a GPU, lite
// >= 24 GB, fast otherwise.
func Recommend(ramBytes uint64, gpu bool) string {
	const gib = uint64(1 << 30)
	switch {
	case ramBytes >= 64*gib && gpu:
		return "full"
	case ramBytes >= 24*gib:
		return "lite"
	}
	return "fast"
}
