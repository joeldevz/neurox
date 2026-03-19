package embed

import (
	"context"
	"math"
	"testing"
)

func TestSerializeDeserializeF32(t *testing.T) {
	original := []float32{1.0, -0.5, 0.333, 0, math.MaxFloat32}
	blob := SerializeF32(original)
	restored := DeserializeF32(blob)

	if len(restored) != len(original) {
		t.Fatalf("length: got %d, want %d", len(restored), len(original))
	}

	for i := range original {
		if restored[i] != original[i] {
			t.Errorf("index %d: got %f, want %f", i, restored[i], original[i])
		}
	}
}

func TestDeserializeF32InvalidLength(t *testing.T) {
	result := DeserializeF32([]byte{1, 2, 3}) // not multiple of 4
	if result != nil {
		t.Errorf("expected nil for invalid length, got %v", result)
	}
}

func TestCosineSimilarityIdentical(t *testing.T) {
	a := []float32{1, 2, 3}
	sim := CosineSimilarity(a, a)
	if math.Abs(sim-1.0) > 1e-6 {
		t.Errorf("identical vectors: got %f, want 1.0", sim)
	}
}

func TestCosineSimilarityOrthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim) > 1e-6 {
		t.Errorf("orthogonal vectors: got %f, want 0.0", sim)
	}
}

func TestCosineSimilarityOpposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim+1.0) > 1e-6 {
		t.Errorf("opposite vectors: got %f, want -1.0", sim)
	}
}

func TestCosineSimilarityDifferentLengths(t *testing.T) {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	sim := CosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("different lengths: got %f, want 0.0", sim)
	}
}

func TestDisabledProvider(t *testing.T) {
	p := Disabled{}
	if _, err := p.Embed(context.Background(), "test"); err == nil {
		t.Error("expected error from disabled provider")
	}
	if p.Dimensions() != 0 {
		t.Error("expected 0 dimensions")
	}
	if p.Name() != "disabled" {
		t.Error("expected 'disabled' name")
	}
	if IsAvailable(p) {
		t.Error("disabled provider should not be available")
	}
}

// MockProvider for testing the queue
type MockProvider struct {
	dims       int
	embedCalls int
}

func (m *MockProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	m.embedCalls++
	vec := make([]float32, m.dims)
	for i := range vec {
		vec[i] = float32(i) * 0.01
	}
	return vec, nil
}

func (m *MockProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	m.embedCalls++
	result := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, m.dims)
		for j := range vec {
			vec[j] = float32(j+i) * 0.01
		}
		result[i] = vec
	}
	return result, nil
}

func (m *MockProvider) Dimensions() int { return m.dims }
func (m *MockProvider) Name() string    { return "mock" }

func TestMockProviderIsAvailable(t *testing.T) {
	p := &MockProvider{dims: 384}
	if !IsAvailable(p) {
		t.Error("mock provider should be available")
	}
}
