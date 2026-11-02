package memeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

const descriptionEmbeddingPolicy = "utf8_32768_mean_l2_v1"
const maxEmbeddingTextBytes = 32 << 10

type publicationText struct {
	text        string
	description bool
}

// Cache only original descriptions within this publication attempt. Existing
// validated documents seed it on resume; no encoding fingerprint is changed.
func (c *Client) embedPublicationBatch(ctx context.Context, generation string, input []publicationText, cache map[[32]byte][]float32) ([][]float32, error) {
	vectors := make([][]float32, len(input))
	var direct []string
	var positions []int
	pending := map[[32]byte]bool{}
	for i, item := range input {
		if !item.description {
			direct = append(direct, item.text)
			positions = append(positions, i)
			continue
		}
		hash := sha256.Sum256([]byte(item.text))
		if _, ok := cache[hash]; ok || pending[hash] {
			continue
		}
		if len(item.text) > maxEmbeddingTextBytes {
			vector, err := c.embedLongDescription(ctx, generation, item.text, hash)
			if err != nil {
				return nil, err
			}
			cache[hash] = vector
		} else {
			pending[hash] = true
			direct = append(direct, item.text)
			positions = append(positions, i)
		}
	}
	if len(direct) > 0 {
		embedded, err := c.embed(ctx, direct)
		if err != nil {
			return nil, err
		}
		for i, pos := range positions {
			vectors[pos] = embedded[i]
			if input[pos].description {
				cache[sha256.Sum256([]byte(input[pos].text))] = embedded[i]
			}
		}
	}
	for i, item := range input {
		if item.description {
			vectors[i] = slices.Clone(cache[sha256.Sum256([]byte(item.text))])
		}
	}
	return vectors, nil
}

// All bytes participate, with UTF-8 boundaries preserved. Short descriptions
// use their original provider vector unchanged; only long ones are averaged.
func (c *Client) embedLongDescription(ctx context.Context, generation, text string, hash [32]byte) ([]float32, error) {
	if !utf8.ValidString(text) {
		return nil, ErrEmbedding
	}
	var chunks []string
	for len(text) > 0 {
		end := min(len(text), maxEmbeddingTextBytes)
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	event := publicationEvent{Generation: generation, Operation: "embed_description", TextHash: hex.EncodeToString(hash[:]), ChunkCount: len(chunks), EmbeddingPolicy: descriptionEmbeddingPolicy}
	event.Fingerprint, _ = memes.Fingerprint(event)
	if err := c.record(ctx, event, "intent"); err != nil {
		return nil, err
	}
	mean := make([]float64, c.options.Dimensions)
	for start := 0; start < len(chunks); start += 64 {
		vectors, err := c.embed(ctx, chunks[start:min(start+64, len(chunks))])
		if err != nil {
			return nil, err
		}
		for _, vector := range vectors {
			for i, value := range vector {
				mean[i] += float64(value) / float64(len(chunks))
			}
		}
	}
	norm := 0.0
	for _, value := range mean {
		norm += value * value
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, ErrEmbedding
	}
	norm = math.Sqrt(norm)
	out := make([]float32, len(mean))
	for i, value := range mean {
		out[i] = float32(value / norm)
	}
	if !validVector(out, c.options.Dimensions) {
		return nil, ErrEmbedding
	}
	if err := c.record(ctx, event, "receipt"); err != nil {
		return nil, err
	}
	return out, nil
}
