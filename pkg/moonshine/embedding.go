package moonshine

import (
	"encoding/json"
	"fmt"
	"runtime"
	"unsafe"
)

// EmbeddingModelArch represents the embedding model architecture.
type EmbeddingModelArch uint32

const (
	// EmbeddingModelArchGemma300M is the Gemma-300M embedding model architecture.
	EmbeddingModelArchGemma300M EmbeddingModelArch = 0
)

const (
	// DefaultEmbeddingVariant is the default quantization variant ("q4").
	DefaultEmbeddingVariant = "q4"
	// DefaultEmbeddingModelName is the default embedding model identifier ("embeddinggemma-300m").
	DefaultEmbeddingModelName = "embeddinggemma-300m"
)

// EmbeddingModel wraps a moonshine text embedder handle (moonshine_create_embedding_model /
// moonshine_create_embedding_model_from_memory). It computes normalized sentence
// embeddings and cosine similarity distances, satisfying agentflow.EmbeddingBackend.
type EmbeddingModel struct {
	handle  int32
	closed  bool
	buffers [][]byte // backing buffer references kept alive while handle is open (from-memory)
}

// NewEmbeddingModel loads an embedding model from files on disk in modelDir.
// variant specifies the model quantization variant ("q4" or "q8"; empty defaults to "q4").
// arch is the model architecture (currently only EmbeddingModelArchGemma300M is supported).
func NewEmbeddingModel(modelDir string, arch EmbeddingModelArch, variant string) (*EmbeddingModel, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	if modelDir == "" {
		return nil, fmt.Errorf("moonshine: modelDir cannot be empty")
	}
	if variant == "" {
		variant = DefaultEmbeddingVariant
	}

	var varPtr *byte
	var varBuf []byte
	if variant != "" {
		varPtr, varBuf = cString(variant)
	}

	h := fnCreateEmbeddingModel(modelDir, uint32(arch), varPtr)
	runtime.KeepAlive(varBuf)

	handle, err := checkHandle("create_embedding_model", h)
	if err != nil {
		return nil, err
	}

	m := &EmbeddingModel{handle: handle}
	runtime.SetFinalizer(m, func(em *EmbeddingModel) { _ = em.Close() })
	return m, nil
}

// NewEmbeddingModelFromMemory creates an embedding model from in-memory buffers.
// files maps canonical filenames (e.g. "model_q4.ort" and "tokenizer.bin") to their raw bytes.
// variant selects the model variant ("q4", "q8"; empty defaults to "q4").
// opts allows passing additional creation options.
func NewEmbeddingModelFromMemory(arch EmbeddingModelArch, variant string, files map[string][]byte, opts ...Option) (*EmbeddingModel, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("moonshine: files map cannot be empty")
	}
	if variant == "" {
		variant = DefaultEmbeddingVariant
	}

	filenames := make([]*byte, 0, len(files))
	memory := make([]*byte, 0, len(files))
	memorySizes := make([]uint64, 0, len(files))
	backingBuffers := make([][]byte, 0, len(files)*2)

	for name, data := range files {
		namePtr, nameBuf := cString(name)
		filenames = append(filenames, namePtr)
		backingBuffers = append(backingBuffers, nameBuf)

		if len(data) > 0 {
			memory = append(memory, &data[0])
			memorySizes = append(memorySizes, uint64(len(data)))
			backingBuffers = append(backingBuffers, data)
		} else {
			memory = append(memory, nil)
			memorySizes = append(memorySizes, 0)
		}
	}

	var varPtr *byte
	var varBuf []byte
	if variant != "" {
		varPtr, varBuf = cString(variant)
	}

	cOpts, optCount, keepOpts := toCOptions(opts)

	h := fnCreateEmbeddingModelFromMemory(
		uint32(arch),
		varPtr,
		unsafe.Pointer(&filenames[0]),
		uint64(len(filenames)),
		unsafe.Pointer(&memory[0]),
		unsafe.Pointer(&memorySizes[0]),
		cOpts,
		optCount,
		HeaderVersion,
	)
	runtime.KeepAlive(varBuf)
	runtime.KeepAlive(filenames)
	runtime.KeepAlive(memory)
	runtime.KeepAlive(memorySizes)
	runtime.KeepAlive(backingBuffers)
	runtime.KeepAlive(keepOpts)

	handle, err := checkHandle("create_embedding_model_from_memory", h)
	if err != nil {
		return nil, err
	}

	m := &EmbeddingModel{
		handle:  handle,
		buffers: backingBuffers,
	}
	runtime.SetFinalizer(m, func(em *EmbeddingModel) { _ = em.Close() })
	return m, nil
}

// CalculateEmbedding computes a normalized float32 feature vector for sentence.
// The returned vector has unit length (~1.0). It satisfies agentflow.EmbeddingBackend.
func (m *EmbeddingModel) CalculateEmbedding(sentence string) ([]float32, error) {
	if !Loaded() {
		return nil, errNotLoaded
	}
	if m.closed {
		return nil, errClosed
	}

	var outPtr unsafe.Pointer
	var outSize uint64

	code := fnCalculateEmbedding(m.handle, sentence, &outPtr, &outSize, nil)
	if err := checkCode("calculate_embedding", code); err != nil {
		return nil, err
	}
	if outPtr == nil || outSize == 0 {
		return nil, nil
	}
	defer freeEmbedding(outPtr)

	return goFloat32Slice((*float32)(outPtr), outSize), nil
}

// Distance calculates cosine similarity between two embedding vectors in [-1, 1].
// It satisfies agentflow.EmbeddingBackend (1 = identical, 0 = orthogonal, -1 = opposite).
func (m *EmbeddingModel) Distance(embA, embB []float32) (float32, error) {
	if !Loaded() {
		return 0, errNotLoaded
	}
	if m.closed {
		return 0, errClosed
	}
	if len(embA) == 0 || len(embB) == 0 {
		return 0, fmt.Errorf("moonshine: embedding vectors cannot be empty")
	}
	if len(embA) != len(embB) {
		return 0, fmt.Errorf("moonshine: embedding vector lengths differ: %d vs %d", len(embA), len(embB))
	}

	var similarity float32
	code := fnCalculateEmbeddingDistance(
		m.handle,
		&embA[0],
		&embB[0],
		uint64(len(embA)),
		&similarity,
	)
	if err := checkCode("calculate_embedding_distance", code); err != nil {
		return 0, err
	}
	return similarity, nil
}

// Close releases the embedding model and all underlying resources. Safe to call more than once.
func (m *EmbeddingModel) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true
	runtime.SetFinalizer(m, nil)
	if fnFreeEmbeddingModel != nil {
		fnFreeEmbeddingModel(m.handle)
	}
	m.buffers = nil
	return nil
}

// GetEmbeddingDependencies returns the download manifest for embedding models
// (moonshine_get_embedding_dependencies). If modelName is empty, the default
// "embeddinggemma-300m" model is used. Recognised options: "variant" ("q4" or "q8").
func GetEmbeddingDependencies(modelName string, opts ...Option) (DependencyManifest, error) {
	if !Loaded() {
		return DependencyManifest{}, errNotLoaded
	}
	var namePtr *byte
	var nameBuf []byte
	if modelName != "" {
		namePtr, nameBuf = cString(modelName)
	}
	cOpts, optCount, keep := toCOptions(opts)
	var outPtr unsafe.Pointer
	code := fnGetEmbeddingDependencies(namePtr, cOpts, optCount, &outPtr)
	runtime.KeepAlive(nameBuf)
	runtime.KeepAlive(keep)
	if err := checkCode("get_embedding_dependencies", code); err != nil {
		return DependencyManifest{}, err
	}
	defer freeC(outPtr)
	raw := goString((*byte)(outPtr))
	var manifest DependencyManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return DependencyManifest{}, fmt.Errorf("moonshine: parsing embedding dependency manifest: %w", err)
	}
	return manifest, nil
}
