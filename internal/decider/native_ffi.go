package decider

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/jupiterrider/ffi"
)

// llamaParams mirrors llama.h from the pinned b11714 release. Keep these field
// lists aligned with internal/llamacpp/manifest.json when repinning.
var llamaModelParamsType = ffi.NewType(
	&ffi.TypePointer, &ffi.TypePointer, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32,
	&ffi.TypePointer, &ffi.TypePointer, &ffi.TypePointer, &ffi.TypePointer,
	&ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8,
)
var llamaContextParamsType = ffi.NewType(
	&ffi.TypeUint32, &ffi.TypeUint32, &ffi.TypeUint32, &ffi.TypeUint32, &ffi.TypeUint32, &ffi.TypeUint32, &ffi.TypeUint32,
	&ffi.TypeSint32, &ffi.TypeSint32,
	&ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32,
	&ffi.TypeFloat, &ffi.TypeFloat, &ffi.TypeFloat, &ffi.TypeFloat, &ffi.TypeFloat, &ffi.TypeFloat,
	&ffi.TypeUint32, &ffi.TypeFloat,
	&ffi.TypePointer, &ffi.TypePointer, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeUint64,
	&ffi.TypePointer, &ffi.TypePointer,
	&ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8, &ffi.TypeUint8,
	&ffi.TypePointer, &ffi.TypeUint64, &ffi.TypePointer,
)
var llamaBatchType = ffi.NewType(&ffi.TypeSint32, &ffi.TypePointer, &ffi.TypePointer, &ffi.TypePointer, &ffi.TypePointer, &ffi.TypePointer, &ffi.TypePointer)

type cBatch struct {
	NTokens                                 int32
	Token, Embd, Pos, NSeqID, SeqID, Logits unsafe.Pointer
}

type llamaAPI struct {
	lib                                             ffi.Lib
	backendInit, modelDefault, modelLoad, modelFree ffi.Fun
	contextDefault, contextNew, contextFree         ffi.Fun
	batchInit, batchFree, decodeFn                  ffi.Fun
	getMemory, memoryClear, getLogits               ffi.Fun
	modelVocab, vocabTokens, tokenizeFn             ffi.Fun
}

var backendOnce sync.Once
var backendErr error

func openLlama(path string) (*llamaAPI, error) {
	l, err := ffi.Load(path)
	if err != nil {
		return nil, err
	}
	a := &llamaAPI{lib: l}
	prep := func(name string, ret *ffi.Type, args ...*ffi.Type) (ffi.Fun, error) {
		f, e := l.Prep(name, ret, args...)
		if e != nil {
			return ffi.Fun{}, fmt.Errorf("llama.cpp symbol %s: %w", name, e)
		}
		return f, nil
	}
	if a.backendInit, err = prep("llama_backend_init", &ffi.TypeVoid); err != nil {
		goto fail
	}
	if a.modelDefault, err = prep("llama_model_default_params", &llamaModelParamsType); err != nil {
		goto fail
	}
	if a.modelLoad, err = prep("llama_model_load_from_file", &ffi.TypePointer, &ffi.TypePointer, &llamaModelParamsType); err != nil {
		goto fail
	}
	if a.modelFree, err = prep("llama_model_free", &ffi.TypeVoid, &ffi.TypePointer); err != nil {
		goto fail
	}
	if a.contextDefault, err = prep("llama_context_default_params", &llamaContextParamsType); err != nil {
		goto fail
	}
	if a.contextNew, err = prep("llama_init_from_model", &ffi.TypePointer, &ffi.TypePointer, &llamaContextParamsType); err != nil {
		goto fail
	}
	if a.contextFree, err = prep("llama_free", &ffi.TypeVoid, &ffi.TypePointer); err != nil {
		goto fail
	}
	if a.batchInit, err = prep("llama_batch_init", &llamaBatchType, &ffi.TypeSint32, &ffi.TypeSint32, &ffi.TypeSint32); err != nil {
		goto fail
	}
	if a.batchFree, err = prep("llama_batch_free", &ffi.TypeVoid, &llamaBatchType); err != nil {
		goto fail
	}
	if a.decodeFn, err = prep("llama_decode", &ffi.TypeSint32, &ffi.TypePointer, &llamaBatchType); err != nil {
		goto fail
	}
	if a.getMemory, err = prep("llama_get_memory", &ffi.TypePointer, &ffi.TypePointer); err != nil {
		goto fail
	}
	if a.memoryClear, err = prep("llama_memory_clear", &ffi.TypeVoid, &ffi.TypePointer, &ffi.TypeUint8); err != nil {
		goto fail
	}
	if a.getLogits, err = prep("llama_get_logits_ith", &ffi.TypePointer, &ffi.TypePointer, &ffi.TypeSint32); err != nil {
		goto fail
	}
	if a.modelVocab, err = prep("llama_model_get_vocab", &ffi.TypePointer, &ffi.TypePointer); err != nil {
		goto fail
	}
	if a.vocabTokens, err = prep("llama_vocab_n_tokens", &ffi.TypeSint32, &ffi.TypePointer); err != nil {
		goto fail
	}
	if a.tokenizeFn, err = prep("llama_tokenize", &ffi.TypeSint32, &ffi.TypePointer, &ffi.TypePointer, &ffi.TypeSint32, &ffi.TypePointer, &ffi.TypeSint32, &ffi.TypeUint8, &ffi.TypeUint8); err != nil {
		goto fail
	}
	backendOnce.Do(func() {
		a.backendInit.Call(nil)
		loadBackends(path)
		runtime.KeepAlive(&a)
	})
	return a, nil
fail:
	_ = l.Close()
	return nil, err
}

func (a *llamaAPI) load(path string, ctxSize int32) (model, context unsafe.Pointer, batch cBatch, vocab unsafe.Pointer, nvocab int32, err error) {
	var mp llamaModelParams
	a.modelDefault.Call(unsafe.Pointer(&mp))
	// All available GPU layers are offloaded when the selected shared library has a GPU backend.
	mp.NGPULayers = -1
	name := append([]byte(path), 0)
	cpath := unsafe.Pointer(&name[0])
	a.modelLoad.Call(unsafe.Pointer(&model), unsafe.Pointer(&cpath), unsafe.Pointer(&mp))
	runtime.KeepAlive(name)
	if model == nil {
		return nil, nil, cBatch{}, nil, 0, errors.New("llama_model_load_from_file failed")
	}
	var cp llamaContextParams
	a.contextDefault.Call(unsafe.Pointer(&cp))
	cp.NCtx = uint32(ctxSize)
	cp.NBatch = uint32(ctxSize)
	cp.NUbatch = 2048
	cp.NSeqMax = 1
	cp.KVUnified = 1
	a.contextNew.Call(unsafe.Pointer(&context), unsafe.Pointer(&model), unsafe.Pointer(&cp))
	if context == nil {
		a.modelFree.Call(nil, unsafe.Pointer(&model))
		return nil, nil, cBatch{}, nil, 0, errors.New("llama_init_from_model failed")
	}
	n := ctxSize
	embd := int32(0)
	seq := int32(1)
	a.batchInit.Call(unsafe.Pointer(&batch), unsafe.Pointer(&n), unsafe.Pointer(&embd), unsafe.Pointer(&seq))
	if batch.Token == nil || batch.Pos == nil || batch.NSeqID == nil || batch.SeqID == nil || batch.Logits == nil {
		a.contextFree.Call(nil, unsafe.Pointer(&context))
		a.modelFree.Call(nil, unsafe.Pointer(&model))
		return nil, nil, cBatch{}, nil, 0, errors.New("llama_batch_init failed")
	}
	a.modelVocab.Call(unsafe.Pointer(&vocab), unsafe.Pointer(&model))
	a.vocabTokens.Call(unsafe.Pointer(&nvocab), unsafe.Pointer(&vocab))
	if vocab == nil || nvocab <= 0 {
		a.batchFree.Call(nil, unsafe.Pointer(&batch))
		a.contextFree.Call(nil, unsafe.Pointer(&context))
		a.modelFree.Call(nil, unsafe.Pointer(&model))
		return nil, nil, cBatch{}, nil, 0, errors.New("llama model has no vocabulary")
	}
	return
}

// llamaModelParams is the layout of the b11714 llama_model_params struct.
type llamaModelParams struct {
	Devices, TensorOverrides                                      unsafe.Pointer
	NGPULayers, SplitMode, LoadMode, LazyMode, MainGPU            int32
	TensorSplit, Progress, ProgressData, KVOverrides              unsafe.Pointer
	VocabOnly, CheckTensors, ExtraBufts, NoHost, NoAlloc, LoadMTP uint8
}

// llamaContextParams mirrors the full b11714 struct, including fields after the v0.7 ABI.
type llamaContextParams struct {
	NCtx, NBatch, NUbatch, NSeqMax, NRSSeq, NOutputsMax, NOutputsMaxPerSeq     uint32
	NThreads, NThreadsBatch                                                    int32
	CtxType, RopeType, PoolingType, AttentionType, FlashType                   int32
	RopeFreqBase, RopeFreqScale, YarnExt, YarnAttn, YarnBetaFast, YarnBetaSlow float32
	YarnOrigCtx                                                                uint32
	Defrag                                                                     float32
	Eval, EvalData                                                             unsafe.Pointer
	TypeK, TypeV                                                               int32
	MoECache                                                                   uint64
	Abort, AbortData                                                           unsafe.Pointer
	Embeddings, OffloadKQV, NoPerf, OpOffload, SWAFul, KVUnified               uint8
	Samplers                                                                   unsafe.Pointer
	NSamplers                                                                  uint64
	OtherContext                                                               unsafe.Pointer
}

func (a *llamaAPI) tokenize(vocab unsafe.Pointer, s string) ([]int32, error) {
	return a.tokenizeMode(vocab, s, false)
}

func (a *llamaAPI) tokenizeSpecial(vocab unsafe.Pointer, s string) ([]int32, error) {
	return a.tokenizeMode(vocab, s, true)
}

func (a *llamaAPI) tokenizeMode(vocab unsafe.Pointer, s string, parseSpecial bool) ([]int32, error) {
	b := []byte(s)
	var bp *byte
	if len(b) > 0 {
		bp = &b[0]
	}
	bl := int32(len(b))
	var n int32
	var noTokens unsafe.Pointer
	var cap int32
	add, parse := uint8(0), uint8(0)
	if parseSpecial {
		parse = 1
	}
	a.tokenizeFn.Call(unsafe.Pointer(&n), unsafe.Pointer(&vocab), unsafe.Pointer(&bp), unsafe.Pointer(&bl), unsafe.Pointer(&noTokens), unsafe.Pointer(&cap), unsafe.Pointer(&add), unsafe.Pointer(&parse))
	if n >= 0 {
		return nil, errors.New("llama_tokenize size query did not return required capacity")
	}
	cap = -n
	if cap <= 0 {
		return []int32{}, nil
	}
	ids := make([]int32, cap)
	ip := unsafe.Pointer(unsafe.SliceData(ids))
	max := cap
	a.tokenizeFn.Call(unsafe.Pointer(&n), unsafe.Pointer(&vocab), unsafe.Pointer(&bp), unsafe.Pointer(&bl), unsafe.Pointer(&ip), unsafe.Pointer(&max), unsafe.Pointer(&add), unsafe.Pointer(&parse))
	runtime.KeepAlive(b)
	if n < 0 || n > max {
		return nil, fmt.Errorf("llama_tokenize failed (%d)", n)
	}
	return ids[:n], nil
}

func (a *llamaAPI) decode(modelCtx, memory unsafe.Pointer, b cBatch, ids []int32, slots []bool, letters []int32, nVocab int32) ([][]float32, error) {
	var clear uint8 = 1
	a.memoryClear.Call(nil, unsafe.Pointer(&memory), unsafe.Pointer(&clear))
	n := len(ids)
	toks := unsafe.Slice((*int32)(b.Token), n)
	pos := unsafe.Slice((*int32)(b.Pos), n)
	nseq := unsafe.Slice((*int32)(b.NSeqID), n)
	seqptr := unsafe.Slice((*unsafe.Pointer)(b.SeqID), n)
	logits := unsafe.Slice((*int8)(b.Logits), n)
	for i, id := range ids {
		toks[i] = id
		pos[i] = int32(i)
		nseq[i] = 1
		if seqptr[i] == nil {
			return nil, errors.New("llama batch has nil sequence-id storage")
		}
		*(*int32)(seqptr[i]) = 0
		if slots[i] {
			logits[i] = 1
		} else {
			logits[i] = 0
		}
	}
	b.NTokens = int32(n)
	var rc int32
	a.decodeFn.Call(unsafe.Pointer(&rc), unsafe.Pointer(&modelCtx), unsafe.Pointer(&b))
	if rc != 0 {
		return nil, fmt.Errorf("llama_decode returned %d", rc)
	}
	out := make([][]float32, 0, len(slots))
	for i, want := range slots {
		if !want {
			continue
		}
		var row unsafe.Pointer
		idx := int32(i)
		a.getLogits.Call(unsafe.Pointer(&row), unsafe.Pointer(&modelCtx), unsafe.Pointer(&idx))
		if row == nil {
			return nil, fmt.Errorf("llama_get_logits_ith(%d) returned null", i)
		}
		all := unsafe.Slice((*float32)(row), int(nVocab))
		r := make([]float32, len(letters))
		for j, id := range letters {
			if id < 0 || id >= nVocab {
				return nil, fmt.Errorf("answer token id %d out of range", id)
			}
			r[j] = all[id]
		}
		out = append(out, r)
	}
	return out, nil
}

// loadBackends registers the compute backends that a release build ships as separate plugin
// libraries (libggml-cpu-*.so, libggml-vulkan.so, ...). Without this llama.cpp reports "no
// backends are loaded" and no model can load. Builds that link their backends in have nothing
// to load here, so every step is best effort.
func loadBackends(llamaPath string) {
	dir := filepath.Dir(llamaPath)
	name := strings.Replace(filepath.Base(llamaPath), "llama", "ggml", 1)
	l, err := ffi.Load(filepath.Join(dir, name))
	if err != nil {
		return
	}
	f, err := l.Prep("ggml_backend_load_all_from_path", &ffi.TypeVoid, &ffi.TypePointer)
	if err != nil {
		return
	}
	cdir := append([]byte(dir), 0)
	p := unsafe.Pointer(&cdir[0])
	f.Call(nil, unsafe.Pointer(&p))
	runtime.KeepAlive(cdir)
}
