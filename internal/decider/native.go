package decider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"

	"github.com/MQ37/nod/internal/llamacpp"
)

const nativeContextSize = 8192
const maxContextTokens = 1536

type chatFile struct {
	Layout  string  `json:"layout"`
	Head    string  `json:"head"`
	Tail    string  `json:"tail"`
	Answer  string  `json:"answer"`
	HeadIDs []int32 `json:"head_ids"`
	TailIDs []int32 `json:"tail_ids"`
}

func chatAnswerText(k int, multi bool, answer string) string {
	if answer == "" {
		answer = "Answer"
	}
	prefix, number := "", ""
	if k > 0 {
		prefix = "\n"
	}
	if multi {
		number = " " + strconv.Itoa(k+1)
	}
	return prefix + answer + number + ": ("
}

// NativeLoader returns a managed-loader function for an installed GGUF.
// libDir may be empty, in which case the standard llama.cpp search locations apply.
func NativeLoader(modelPath, libDir string) Loader {
	return func(context.Context) (ClosableDecider, error) { return LoadNative(modelPath, libDir) }
}

// NativeDecider evaluates all schema fields in one llama.cpp decode pass.
type NativeDecider struct {
	mu                        sync.Mutex
	api                       *llamaAPI
	model, ctx, vocab, memory unsafe.Pointer
	batch                     cBatch
	nVocab                    int32
	temps                     Temps
	file                      fileConfig
	layout                    string
	chatHead, chatTail        []int32
	chatAnswer                string
	tokenizeText              func(string) ([]int32, error)
	labels                    []string
	labelIDs                  []int32
	closed                    bool
}

// LoadNative loads a GGUF and its decider_config.json with llama.cpp.
func LoadNative(modelPath, libDir string) (*NativeDecider, error) {
	if modelPath == "" {
		return nil, errors.New("model path is empty")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(modelPath), "decider_config.json"))
	if err != nil {
		return nil, fmt.Errorf("read decider_config.json: %w", err)
	}
	tokJSON, err := os.ReadFile(filepath.Join(filepath.Dir(modelPath), "tokenizer.json"))
	if err != nil {
		return nil, fmt.Errorf("read tokenizer.json: %w", err)
	}
	if !json.Valid(tokJSON) {
		return nil, errors.New("tokenizer.json is invalid JSON")
	}
	tokJSON = nil
	temps, cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	layout := cfg.Layout
	var chat chatFile
	if layout == "" {
		layout = "plain"
	}
	if layout == "chat" {
		b, e := os.ReadFile(filepath.Join(filepath.Dir(modelPath), "decider_chat.json"))
		if e != nil {
			return nil, fmt.Errorf("read decider_chat.json: %w", e)
		}
		if e = json.Unmarshal(b, &chat); e != nil {
			return nil, fmt.Errorf("decider_chat.json: %w", e)
		}
		if chat.Layout != "chat" || chat.Head == "" || chat.Tail == "" {
			return nil, errors.New("decider_chat.json must define chat layout head and tail")
		}
		if chat.Answer == "" {
			chat.Answer = "Answer"
		}
	} else if layout != "plain" {
		return nil, fmt.Errorf("unsupported decider prompt layout %q", layout)
	}
	if libDir == "" {
		libDir = os.Getenv("NOD_LLAMA_LIB_DIR")
	}
	libDir, ok := llamacpp.Locate(libDir, "", "")
	if !ok {
		return nil, errors.New("llama.cpp shared library not found")
	}
	libPath := filepath.Join(libDir, llamacpp.LibName())
	fmt.Fprintln(os.Stderr, "nod: loading Decider model")
	a, err := openLlama(libPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nod: Decider load failed: %v\n", err)
		return nil, err
	}
	model, ctx, batch, vocab, nv, err := a.load(modelPath, nativeContextSize)
	if err != nil {
		_ = a.lib.Close()
		fmt.Fprintf(os.Stderr, "nod: Decider load failed: %v\n", err)
		return nil, err
	}
	var memory unsafe.Pointer
	a.getMemory.Call(unsafe.Pointer(&memory), unsafe.Pointer(&ctx))
	if memory == nil {
		a.batchFree.Call(nil, unsafe.Pointer(&batch))
		a.contextFree.Call(nil, unsafe.Pointer(&ctx))
		a.modelFree.Call(nil, unsafe.Pointer(&model))
		_ = a.lib.Close()
		return nil, errors.New("llama_get_memory returned null")
	}
	d := &NativeDecider{api: a, model: model, ctx: ctx, batch: batch, vocab: vocab, memory: memory, nVocab: nv, temps: temps, file: cfg, layout: layout, chatAnswer: chat.Answer}
	d.tokenizeText = func(s string) ([]int32, error) {
		if layout == "chat" {
			return a.tokenizeSpecial(vocab, s)
		}
		return a.tokenize(vocab, s)
	}
	if layout == "chat" {
		d.chatHead, err = a.tokenizeSpecial(vocab, chat.Head)
		if err == nil {
			d.chatTail, err = a.tokenizeSpecial(vocab, chat.Tail)
		}
		if err == nil && len(chat.HeadIDs) > 0 && !sameIDs(d.chatHead, chat.HeadIDs) {
			err = errors.New("decider_chat.json head token ids do not match GGUF vocabulary")
		}
		if err == nil && len(chat.TailIDs) > 0 && !sameIDs(d.chatTail, chat.TailIDs) {
			err = errors.New("decider_chat.json tail token ids do not match GGUF vocabulary")
		}
		if err != nil {
			_ = d.Close()
			return nil, err
		}
	}
	if err := d.makeLabels(); err != nil {
		_ = d.Close()
		return nil, err
	}
	if layout == "chat" {
		if err := d.validateChatTokens(chat.Tail); err != nil {
			_ = d.Close()
			return nil, err
		}
	}
	fmt.Fprintln(os.Stderr, "nod: Decider model loaded")
	return d, nil
}

func (d *NativeDecider) makeLabels() error {
	seen := map[int32]bool{}
	for c := 'A'; c <= 'Z' && len(d.labels) < 255; c++ {
		d.addLabel(string(c), seen)
	}
	for a := 'A'; a <= 'Z' && len(d.labels) < 255; a++ {
		for b := 'A'; b <= 'Z' && len(d.labels) < 255; b++ {
			d.addLabel(string([]rune{a, b}), seen)
		}
	}
	if len(d.labels) < 255 {
		return fmt.Errorf("llama vocab has only %d one-token answer labels; need 255", len(d.labels))
	}
	return nil
}
func (d *NativeDecider) addLabel(s string, seen map[int32]bool) {
	ids, err := d.api.tokenize(d.vocab, s)
	if err == nil && len(ids) == 1 && !seen[ids[0]] {
		seen[ids[0]] = true
		d.labels = append(d.labels, s)
		d.labelIDs = append(d.labelIDs, ids[0])
	}
}

func (d *NativeDecider) validateChatTokens(tailText string) error {
	prefix := d.chatAnswer + ": ("
	pre, err := d.api.tokenize(d.vocab, prefix)
	if err != nil {
		return err
	}
	combined, err := d.api.tokenizeSpecial(d.vocab, tailText+prefix)
	if err != nil {
		return err
	}
	if !sameIDs(combined, append(append([]int32(nil), d.chatTail...), pre...)) {
		return errors.New("chat layout: tail merges with the answer prefix")
	}
	for i := 0; i < 10; i++ {
		if i >= len(d.labels) {
			return errors.New("chat layout: fewer than ten one-token answer labels")
		}
		joined, e := d.api.tokenize(d.vocab, prefix+d.labels[i])
		if e != nil {
			return e
		}
		if !sameIDs(joined, append(append([]int32(nil), pre...), d.labelIDs[i])) {
			return fmt.Errorf("chat layout: answer label %q does not tokenize as one token after %q", d.labels[i], prefix)
		}
	}
	return nil
}

type slotSpec struct {
	key     string
	field   Field
	options []string
	labels  []string
	ids     []int32
	kind    string
}

func (d *NativeDecider) Decide(contextText string, schema Schema) (map[string]Answer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("decider is closed")
	}
	if len(schema.Order) == 0 {
		return map[string]Answer{}, nil
	}
	ids, slots, specs, err := d.build(contextText, schema)
	if err != nil {
		return nil, err
	}
	if len(ids) > nativeContextSize {
		return nil, fmt.Errorf("decider prompt exceeds %d token context", nativeContextSize)
	}
	want := make([]bool, len(ids))
	for _, s := range slots {
		want[s] = true
	}
	logits, err := d.api.decode(d.ctx, d.memory, d.batch, ids, want, d.labelIDs, d.nVocab)
	if err != nil {
		return nil, err
	}
	types, nopts := make([]string, len(specs)), make([]int, len(specs))
	for i, s := range specs {
		types[i] = s.kind
		nopts[i] = len(s.options)
	}
	Ts := d.temps.ForSlots(types, nopts)
	out := make(map[string]Answer, len(specs))
	for i, s := range specs {
		if i >= len(logits) {
			return nil, errors.New("missing answer-slot logits")
		}
		if len(logits[i]) < len(s.options) {
			return nil, errors.New("answer-slot logits have fewer columns than schema options")
		}
		probs := softmax(logits[i][:len(s.options)], Ts[i])
		ans := makeAnswer(s, probs, d.file.AbstainBelow)
		out[s.key] = ans
	}
	return out, nil
}

func (d *NativeDecider) build(contextText string, schema Schema) ([]int32, []int, []slotSpec, error) {
	tokenize := d.tokenizeText
	if tokenize == nil {
		tokenize = func(s string) ([]int32, error) { return d.api.tokenize(d.vocab, s) }
	}
	ctx, err := tokenize("Context:\n" + contextText)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(ctx) > maxContextTokens {
		ctx = ctx[:maxContextTokens]
	}
	ids := append([]int32(nil), ctx...)
	if d.layout == "chat" {
		ids = append(append([]int32(nil), d.chatHead...), ctx...)
	}
	slots := make([]int, 0, len(schema.Order))
	specs := make([]slotSpec, 0, len(schema.Order))
	multi := len(schema.Order) > 1
	open, _ := tokenize("\n(")
	for k, key := range schema.Order {
		f, ok := schema.Fields[key]
		if !ok {
			return nil, nil, nil, fmt.Errorf("schema[%q] is missing", key)
		}
		options := append([]string(nil), f.Options...)
		if len(options) == 0 {
			return nil, nil, nil, fmt.Errorf("schema[%q] has no options", key)
		}
		if len(options) > 255 {
			return nil, nil, nil, fmt.Errorf("schema[%q] has more than 255 options", key)
		}
		kind := f.Type
		if kind == "bool" {
			kind = TypeNoul
		}
		if kind == "scale" {
			kind = TypeScore
		}
		if kind != TypeChoice && kind != TypeNoul && kind != TypeScore {
			return nil, nil, nil, fmt.Errorf("schema[%q] has unsupported type %q", key, f.Type)
		}
		labelNames := d.labels[:len(options)]
		labelTokens := d.labelIDs[:len(options)]
		var piece []int32
		head := fmt.Sprintf("\n\nQuestion%s: %s\nOptions:", questionNumber(k, multi), key)
		if d.layout == "chat" {
			piece, err = tokenize(head)
			if err == nil && len(options) <= 10 {
				var b = ""
				for i, o := range options {
					b += fmt.Sprintf("\n(%s) %s", labelNames[i], o)
				}
				var enc []int32
				enc, err = tokenize(b)
				piece = append(piece, enc...)
			} else if err == nil {
				for i, o := range options {
					enc, e := tokenize(") " + o)
					if e != nil {
						err = e
						break
					}
					piece = append(piece, open...)
					piece = append(piece, labelTokens[i])
					piece = append(piece, enc...)
				}
			}
		} else if len(options) <= 10 {
			var b = head
			for i, o := range options {
				b += fmt.Sprintf("\n(%s) %s", labelNames[i], o)
			}
			b += fmt.Sprintf("\nAnswer%s: (", questionNumber(k, multi))
			piece, err = tokenize(b)
			if err != nil {
				return nil, nil, nil, err
			}
		} else {
			piece, err = tokenize(head)
			if err != nil {
				return nil, nil, nil, err
			}
			for i, o := range options {
				enc, err := tokenize(") " + o)
				if err != nil {
					return nil, nil, nil, err
				}
				piece = append(piece, open...)
				piece = append(piece, labelTokens[i])
				piece = append(piece, enc...)
			}
			tail, err := tokenize(fmt.Sprintf("\nAnswer%s: (", questionNumber(k, multi)))
			if err != nil {
				return nil, nil, nil, err
			}
			piece = append(piece, tail...)
		}
		if len(piece) == 0 {
			return nil, nil, nil, fmt.Errorf("schema[%q] produced an empty prompt", key)
		}
		ids = append(ids, piece...)
		if d.layout != "chat" {
			slots = append(slots, len(ids)-1)
		}
		specs = append(specs, slotSpec{key: key, field: f, options: options, labels: labelNames, ids: labelTokens, kind: kind})
	}
	if d.layout == "chat" {
		ids = append(ids, d.chatTail...)
		for k := range specs {
			piece, e := tokenize(chatAnswerText(k, multi, d.chatAnswer))
			if e != nil {
				return nil, nil, nil, e
			}
			if len(piece) == 0 {
				return nil, nil, nil, fmt.Errorf("empty chat answer piece")
			}
			ids = append(ids, piece...)
			slots = append(slots, len(ids)-1)
		}
	}
	return ids, slots, specs, nil
}

func sameIDs(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func questionNumber(k int, multi bool) string {
	if !multi {
		return ""
	}
	return " " + strconv.Itoa(k+1)
}

func softmax(logits []float32, temp float64) []float64 {
	if temp <= 0 || math.IsNaN(temp) || math.IsInf(temp, 0) {
		temp = 1
	}
	max := math.Inf(-1)
	for _, x := range logits {
		v := float64(x) / temp
		if v > max {
			max = v
		}
	}
	p := make([]float64, len(logits))
	sum := 0.0
	for i, x := range logits {
		p[i] = math.Exp(float64(x)/temp - max)
		sum += p[i]
	}
	if sum == 0 || math.IsInf(sum, 0) {
		return p
	}
	for i := range p {
		p[i] = roundN(p[i]/sum, 4)
	}
	return p
}

func makeAnswer(s slotSpec, p []float64, abstain float64) Answer {
	best := 0
	for i := 1; i < len(p); i++ {
		if p[i] > p[best] {
			best = i
		}
	}
	ps := make([]prob, len(s.options))
	for i, o := range s.options {
		key := o
		if s.kind == TypeScore && len(s.field.Legend.keys) > i {
			key = s.field.Legend.keys[i]
		}
		ps[i] = prob{Key: key, P: p[i]}
	}
	switch s.kind {
	case TypeNoul:
		return Answer{Type: TypeNoul, Noul: p[min(1, len(p)-1)], Probabilities: ps}
	case TypeScore:
		score := 0.0
		for i, x := range p {
			level := float64(i)
			if i < len(s.field.Legend.keys) {
				if n, e := strconv.ParseFloat(s.field.Legend.keys[i], 64); e == nil {
					level = n
				}
			}
			score += level * x
		}
		return Answer{Type: TypeScore, Score: roundN(score, 4), Confidence: p[best], Probabilities: ps, Legend: s.field.Legend.raw}
	default:
		return Answer{Type: TypeChoice, Choice: s.options[best], Confidence: p[best], Probabilities: ps, Abstain: abstain > 0 && p[best] < abstain}
	}
}

// Close releases the batch, context and model (and closes the shared library).
func (d *NativeDecider) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	var errs []error
	d.api.batchFree.Call(nil, unsafe.Pointer(&d.batch))
	d.api.contextFree.Call(nil, unsafe.Pointer(&d.ctx))
	d.api.modelFree.Call(nil, unsafe.Pointer(&d.model))
	if err := d.api.lib.Close(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		fmt.Fprintf(os.Stderr, "nod: Decider unload failed: %v\n", err)
		return err
	}
	fmt.Fprintln(os.Stderr, "nod: Decider model unloaded")
	return nil
}
