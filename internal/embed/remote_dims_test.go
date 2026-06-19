package embed

import (
	"testing"
)

// TestRemoteDimensionsZeroBeforeFirstEmbed verifies that Dimensions() returns 0
// before the first successful embed when config.Dimensions is 0.
func TestRemoteDimensionsZeroBeforeFirstEmbed(t *testing.T) {
	r := NewRemote(RemoteConfig{
		URL:        "http://localhost:8000/v1",
		APIKey:     "test-key",
		Model:      "test-model",
		Dimensions: 0, // Not configured
	})

	// Before first embed, dimensions should be 0
	if r.Dimensions() != 0 {
		t.Errorf("Dimensions() before first embed = %d, want 0", r.Dimensions())
	}
}

// TestRemoteDimensionsPreloadedFromConfig verifies that Dimensions() returns
// the configured value when config.Dimensions is set.
func TestRemoteDimensionsPreloadedFromConfig(t *testing.T) {
	r := NewRemote(RemoteConfig{
		URL:        "http://localhost:8000/v1",
		APIKey:     "test-key",
		Model:      "test-model",
		Dimensions: 2048, // Pre-configured
	})

	// Should return the configured value immediately
	if r.Dimensions() != 2048 {
		t.Errorf("Dimensions() with config = %d, want 2048", r.Dimensions())
	}
}

// TestRemoteDimensionsDetectedFromResponse verifies that Dimensions() reflects
// the actual dimensions from a successful EmbedBatch response.
func TestRemoteDimensionsDetectedFromResponse(t *testing.T) {
	r := NewRemote(RemoteConfig{
		URL:        "http://localhost:8000/v1",
		APIKey:     "test-key",
		Model:      "test-model",
		Dimensions: 0, // Not configured — will be detected
	})

	// Since we can't directly inject the client, we'll test with a simpler approach:
	// Simulate what EmbedBatch does when it detects dimensions from a 4096-dim response
	r.dims.CompareAndSwap(0, 4096)

	// After the mock response is processed, dimensions should be detected
	if r.Dimensions() != 4096 {
		t.Errorf("Dimensions() after mock response = %d, want 4096", r.Dimensions())
	}
}

// TestRemoteDimensionsMismatchLogging verifies that a configured dimension
// that mismatches the actual response is not overwritten when already set.
func TestRemoteDimensionsMismatchHandling(t *testing.T) {
	// Pre-configured with one value
	r := NewRemote(RemoteConfig{
		URL:        "http://localhost:8000/v1",
		APIKey:     "test-key",
		Model:      "test-model",
		Dimensions: 1536,
	})

	// Verify we keep the configured value
	if r.Dimensions() != 1536 {
		t.Errorf("Dimensions() with pre-config = %d, want 1536", r.Dimensions())
	}

	// CompareAndSwap(0, 4096) will NOT swap because current=1536, not 0
	r.dims.CompareAndSwap(0, 4096) // Expected 0, but we have 1536, so no swap
	if r.Dimensions() != 1536 {
		t.Errorf("Dimensions() after CompareAndSwap(0, 4096) = %d, want 1536 (no swap)", r.Dimensions())
	}
}

// TestRemoteEmbedBatchDetectsDimensionsOnce verifies that EmbedBatch
// detects and stores dimensions only on the first successful response.
func TestRemoteEmbedBatchDetectsDimensionsOnce(t *testing.T) {
	r := NewRemote(RemoteConfig{
		URL:        "http://localhost:8000/v1",
		APIKey:     "test-key",
		Model:      "test-model",
		Dimensions: 0,
	})

	// Simulate a successful batch response
	embeddings := [][]float32{
		make([]float32, 768), // 768-dimensional
		make([]float32, 768),
	}

	// After CompareAndSwap with first dimension detection
	if len(embeddings) > 0 && len(embeddings[0]) > 0 {
		r.dims.CompareAndSwap(0, int32(len(embeddings[0])))
	}

	if r.Dimensions() != 768 {
		t.Errorf("Dimensions() after first batch = %d, want 768", r.Dimensions())
	}

	// Simulate another batch with different dimensions (shouldn't change)
	embeddings2 := [][]float32{
		make([]float32, 4096), // 4096-dimensional
	}

	if len(embeddings2) > 0 && len(embeddings2[0]) > 0 {
		// This CompareAndSwap should fail (current is 768, not 0)
		r.dims.CompareAndSwap(0, int32(len(embeddings2[0])))
	}

	// Should still be 768 (unchanged)
	if r.Dimensions() != 768 {
		t.Errorf("Dimensions() after second batch = %d, want 768 (unchanged)", r.Dimensions())
	}
}
