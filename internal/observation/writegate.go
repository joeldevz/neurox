package observation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

const duplicateWarningThreshold = 0.92

type Embedder interface {
	Available() bool
	Embed(ctx context.Context, text string) ([]float32, error)
}

type WriteGate interface {
	CheckAsync(observation Observation)
}

type SimilarityWarning struct {
	ObservationID string
	SimilarID     string
	Namespace     string
	Score         float64
}

type WarningSink interface {
	Warn(ctx context.Context, warning SimilarityWarning)
}

type LoggerWarningSink struct{}

func (LoggerWarningSink) Warn(_ context.Context, warning SimilarityWarning) {
	log.Printf("write gate warning: observation=%s similar=%s namespace=%s cosine=%.4f", warning.ObservationID, warning.SimilarID, warning.Namespace, warning.Score)
}

type AsyncWriteGate struct {
	db       *sql.DB
	embedder Embedder
	sink     WarningSink
	timeout  time.Duration
}

func NewAsyncWriteGate(database *sql.DB, embedder Embedder, sink WarningSink) *AsyncWriteGate {
	if sink == nil {
		sink = LoggerWarningSink{}
	}
	return &AsyncWriteGate{
		db:       database,
		embedder: embedder,
		sink:     sink,
		timeout:  2 * time.Second,
	}
}

func (g *AsyncWriteGate) CheckAsync(observation Observation) {
	if g == nil || g.db == nil || g.embedder == nil || !g.embedder.Available() {
		return
	}

	go func(obs Observation) {
		ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
		defer cancel()

		embedding, err := g.embedder.Embed(ctx, embeddingInput(obs))
		if err != nil || len(embedding) == 0 {
			return
		}

		rows, err := g.db.QueryContext(ctx, `
			SELECT id, embedding
			FROM observations
			WHERE namespace = ? AND deleted_at IS NULL AND embedding IS NOT NULL AND id != ?
		`, obs.Namespace, obs.ID)
		if err != nil {
			return
		}
		defer rows.Close()

		for rows.Next() {
			var candidateID string
			var rawEmbedding []byte
			if err := rows.Scan(&candidateID, &rawEmbedding); err != nil {
				return
			}
			candidateEmbedding, err := decodeEmbedding(rawEmbedding)
			if err != nil {
				continue
			}
			score := cosineSimilarity(embedding, candidateEmbedding)
			if score > duplicateWarningThreshold {
				g.sink.Warn(ctx, SimilarityWarning{
					ObservationID: obs.ID,
					SimilarID:     candidateID,
					Namespace:     obs.Namespace,
					Score:         score,
				})
			}
		}
	}(observation)
}

type noopWriteGate struct{}

func (noopWriteGate) CheckAsync(Observation) {}

func NewNoopWriteGate() WriteGate {
	return noopWriteGate{}
}

func embeddingInput(observation Observation) string {
	parts := []string{observation.Title, observation.Content}
	if len(observation.Tags) > 0 {
		parts = append(parts, strings.Join(observation.Tags, ", "))
	}
	return strings.Join(parts, "\n\n")
}

func decodeEmbedding(value []byte) ([]float32, error) {
	if len(value)%4 != 0 {
		return nil, fmt.Errorf("invalid embedding size %d", len(value))
	}
	reader := bytes.NewReader(value)
	embedding := make([]float32, len(value)/4)
	if err := binary.Read(reader, binary.LittleEndian, &embedding); err != nil {
		return nil, fmt.Errorf("decode embedding: %w", err)
	}
	return embedding, nil
}

func cosineSimilarity(left []float32, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}

	var dot float64
	var leftNorm float64
	var rightNorm float64
	for index := range left {
		leftValue := float64(left[index])
		rightValue := float64(right[index])
		dot += leftValue * rightValue
		leftNorm += leftValue * leftValue
		rightNorm += rightValue * rightValue
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}
