package embed

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

// Provider generates embeddings for text.
type Provider interface {
	// Embed returns a float32 vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns vectors for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the embedding vector size.
	Dimensions() int

	// Name returns the provider name for logging.
	Name() string
}

// Disabled is a no-op provider when embeddings are not available.
type Disabled struct{}

func (Disabled) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, fmt.Errorf("embedding provider is disabled")
}

func (Disabled) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, fmt.Errorf("embedding provider is disabled")
}

func (Disabled) Dimensions() int { return 0 }
func (Disabled) Name() string    { return "disabled" }

// SerializeF32 converts a float32 slice to bytes for SQLite BLOB storage.
func SerializeF32(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeF32 converts bytes back to a float32 slice.
func DeserializeF32(data []byte) []float32 {
	if len(data)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(data)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vec
}

// CosineSimilarity computes cosine similarity between two vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
