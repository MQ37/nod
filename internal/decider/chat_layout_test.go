package decider

import (
	"encoding/json"
	"os"
	"testing"
)

func TestChatAnswerPieces(t *testing.T) {
	for _, tc := range []struct {
		k     int
		multi bool
		want  string
	}{
		{0, false, "Answer: ("}, {0, true, "Answer 1: ("}, {1, true, "\nAnswer 2: ("},
	} {
		if got := chatAnswerText(tc.k, tc.multi, "Answer"); got != tc.want {
			t.Fatalf("chatAnswerText=%q want %q", got, tc.want)
		}
	}
}

func TestChatBoundaryIDs(t *testing.T) {
	b, err := os.ReadFile("../../testdata/decider/12b/decider_chat.json")
	if err != nil {
		t.Fatal(err)
	}
	var chat chatFile
	if err := json.Unmarshal(b, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Layout != "chat" || !sameIDs(chat.HeadIDs, []int32{2, 105, 2364, 107}) || !sameIDs(chat.TailIDs, []int32{106, 107, 105, 4368, 107, 100, 45518, 107, 101}) {
		t.Fatalf("unexpected chat fixture: %+v", chat)
	}
	if sameIDs(chat.HeadIDs, []int32{2, 105}) {
		t.Fatal("accepted truncated head IDs")
	}
	_, cfg, err := ParseConfig([]byte(`{"layout":"chat","temperature_by_type":{"choice":1.5,"noul":0.05,"score":1.0}}`))
	if err != nil || cfg.Layout != "chat" {
		t.Fatalf("config layout=%q err=%v", cfg.Layout, err)
	}
}

func TestChatBuildWithFakeTokenizer(t *testing.T) {
	var calls []string
	d := &NativeDecider{layout: "chat", chatHead: []int32{2, 105}, chatTail: []int32{106, 107}, chatAnswer: "Answer", labels: []string{"A", "B"}, labelIDs: []int32{90, 91}}
	d.tokenizeText = func(s string) ([]int32, error) {
		calls = append(calls, s)
		return []int32{int32(len(calls) + 200)}, nil
	}
	schema := Schema{Order: []string{"q"}, Fields: map[string]Field{"q": {Type: "choice", Options: []string{"yes", "no"}}}}
	ids, slots, _, err := d.build("state", schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 5 || calls[0] != "Context:\nstate" || calls[1] != "\n(" || calls[2] != "\n\nQuestion: q\nOptions:" || calls[3] != "\n(A) yes\n(B) no" || calls[4] != "Answer: (" {
		t.Fatalf("tokenization calls: %#v", calls)
	}
	want := []int32{2, 105, 201, 203, 204, 106, 107, 205}
	if !sameIDs(ids, want) {
		t.Fatalf("ids=%v want %v", ids, want)
	}
	if len(slots) != 1 || slots[0] != len(ids)-1 {
		t.Fatalf("slots=%v ids=%v", slots, ids)
	}
}
