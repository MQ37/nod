package decider

import (
	"math"
	"testing"
)

func TestScaledSoftmaxMasksUnusedAnswerLabels(t *testing.T) {
	// Python's GGUF readout masks columns >= n_options to -inf before
	// softmax(logits / temperature). With logits ln(8), 0 and T=1,
	// the two option probabilities are exactly 8/9 and 1/9.
	got := softmax([]float32{float32(math.Log(8)), 0}, 1)
	if len(got) != 2 {
		t.Fatalf("got %d probabilities, want 2", len(got))
	}
	if math.Abs(got[0]-0.8889) > 1e-4 || math.Abs(got[1]-0.1111) > 1e-4 {
		t.Fatalf("probabilities = %v, want [0.8889 0.1111]", got)
	}
	if sum := got[0] + got[1]; math.Abs(sum-1) > 1e-12 {
		t.Fatalf("probability sum = %.12f, want 1", sum)
	}
}

func TestScaledSoftmaxUsesPerSlotTemperature(t *testing.T) {
	// Python decider-ai maps each schema answer type to its configured
	// temperature, then applies softmax(logits / T) for that slot.
	temps, _, err := ParseConfig([]byte(`{"temperature":1.4,"temperature_by_type":{"choice":1.48,"noul":2.22,"score":1.38}}`))
	if err != nil {
		t.Fatal(err)
	}
	slots := temps.ForSlots([]string{TypeChoice, TypeNoul, TypeScore}, []int{2, 2, 5})
	if len(slots) != 3 || slots[0] != 1.48 || slots[1] != 2.22 || slots[2] != 1.38 {
		t.Fatalf("slot temperatures = %v", slots)
	}
	got := softmax([]float32{float32(math.Log(8)), 0}, slots[1])
	root := math.Exp(math.Log(8) / slots[1])
	want := []float64{root / (root + 1), 1 / (root + 1)}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-4 {
			t.Fatalf("probabilities = %v, want approximately %v", got, want)
		}
	}
}
