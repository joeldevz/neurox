package benchmark

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

const defaultFakeDimensions = 384

// FakeEmbedder is a deterministic embedding provider for benchmarks and tests.
// It does not call any external service; instead it derives vectors from text
// content using FNV-1a word hashing. Texts that share words produce closer
// vectors (higher cosine similarity), enabling meaningful semantic search tests.
type FakeEmbedder struct {
	dims int
}

// NewFakeEmbedder creates a FakeEmbedder with the given number of dimensions.
// Pass 0 to use the default (384).
func NewFakeEmbedder(dims int) *FakeEmbedder {
	if dims <= 0 {
		dims = defaultFakeDimensions
	}
	return &FakeEmbedder{dims: dims}
}

// Embed returns a deterministic, L2-normalised float32 vector for text.
// The algorithm:
//  1. Tokenise text into lowercase words.
//  2. For each word, compute a per-word float32 vector using FNV-1a hashing
//     to fill each position deterministically.
//  3. Sum all word vectors element-wise.
//  4. L2-normalise the result.
func (f *FakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	words := tokenize(text)
	vec := make([]float32, f.dims)

	if len(words) == 0 {
		// Return a zero-like vector (will be all zeros after no summation)
		// Still normalise to avoid NaN.
		vec[0] = 1.0
		return vec, nil
	}

	for _, word := range words {
		wv := wordVector(word, f.dims)
		for i, v := range wv {
			vec[i] += v
		}
	}

	return l2Normalize(vec), nil
}

// EmbedBatch embeds each text independently. Order is preserved.
func (f *FakeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := f.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}

// Dimensions returns the configured vector size.
func (f *FakeEmbedder) Dimensions() int { return f.dims }

// Name returns the provider identifier.
func (f *FakeEmbedder) Name() string { return "fake-benchmark" }

// --- internal helpers ---

// tokenize splits text into lowercase words, stripping punctuation.
func tokenize(text string) []string {
	lower := strings.ToLower(text)
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return words
}

// wordVector derives a deterministic float32 vector for a single word using
// FNV-1a. Each dimension is seeded from (hash XOR rotated-by-position).
func wordVector(word string, dims int) []float32 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(word))
	seed := h.Sum64()

	vec := make([]float32, dims)
	for i := range vec {
		// Derive a position-specific value by mixing seed with position.
		// Use a simple xorshift-style mixing for each dimension.
		v := seed ^ (seed >> uint(i%63+1)) ^ uint64(i)*0x9e3779b97f4a7c15
		// Map to [-1, 1] by treating as signed fraction.
		// Normalise to [-1.0, 1.0].
		vec[i] = float32(int64(v)) / float32(math.MaxInt64)
	}
	return vec
}

// l2Normalize scales vec so its Euclidean norm is 1.0.
// If the norm is 0 (zero vector), returns the vector unchanged.
func l2Normalize(vec []float32) []float32 {
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return vec
	}
	norm := math.Sqrt(sumSq)
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(float64(v) / norm)
	}
	return out
}
