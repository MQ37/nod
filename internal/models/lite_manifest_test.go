package models

import "testing"

func TestLiteManifestAndConversion(t *testing.T) {
	tier, err := Lookup("lite")
	if err != nil {
		t.Fatal(err)
	}
	if tier.Source != "hf-convert" || tier.Repo != "Mapika/decider-12b" || tier.GGUF != "decider-12b-Q8_0.gguf" || tier.OutType != "q8_0" || tier.GGUFSHA256 != "TODO-sha256" {
		t.Fatalf("unexpected lite tier: %+v", tier)
	}
	files, err := tier.SourceFiles()
	if err != nil {
		t.Fatal(err)
	}
	var model bool
	for _, f := range files {
		if f.Path == "model.safetensors" && f.Size == 23919549408 && f.SHA256 == "efb90aaf7521515851584f673ff4513027abb84df97a9ad2aa4fe27bc250770a" {
			model = true
		}
	}
	if !model {
		t.Fatal("lite safetensors pin missing")
	}
	steps, err := ConvertSteps(tier, "src", "out.gguf")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Name == "patch-block-count" || s.Name == "patch-nextn" {
			t.Fatalf("unexpected 12B metadata patch: %s", s.Name)
		}
	}
}

func TestTierPinned(t *testing.T) {
	if !TierPinned("fast") {
		t.Fatal("fast is pinned")
	}
	if TierPinned("lite") || TierPinned("full") || TierPinned("nope") {
		t.Fatal("lite, full and unknown tiers must not count as pinned")
	}
}
