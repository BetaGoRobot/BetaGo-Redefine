package mememilvus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

type embeddingCheckpoint struct {
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Policy    string    `json:"policy"`
	TextSHA   string    `json:"text_sha"`
	Vector    []float32 `json:"vector"`
}

func (c *Client) vectorKey(text string, description bool) (string, embeddingCheckpoint) {
	h := sha256.Sum256([]byte(text))
	policy := "direct_v1"
	if description {
		policy = descriptionEmbeddingPolicy
	}
	v := embeddingCheckpoint{Model: c.options.EmbeddingModel, Dimension: c.options.Dimensions, Policy: policy, TextSHA: hex.EncodeToString(h[:])}
	fp, _ := memes.Fingerprint(v)
	return "milvus:embedding:" + fp, v
}
func (c *Client) checkpointVectors(ctx context.Context, texts []string, description bool, cache map[string][]float32, readOnly bool) ([][]float32, error) {
	out := make([][]float32, len(texts))
	var missing []int
	var pending []string
	keys := make([]string, len(texts))
	metas := make([]embeddingCheckpoint, len(texts))
	seen := map[string]bool{}
	for i, text := range texts {
		if text == "" || !utf8.ValidString(text) {
			return nil, ErrEmbedding
		}
		keys[i], metas[i] = c.vectorKey(text, description)
		if _, ok := cache[keys[i]]; ok || seen[keys[i]] {
			continue
		}
		var stored embeddingCheckpoint
		found, e := c.get(ctx, keys[i], &stored)
		if e != nil {
			return nil, e
		}
		if found {
			v := stored.Vector
			stored.Vector = nil
			a, _ := memes.Fingerprint(stored)
			b, _ := memes.Fingerprint(metas[i])
			if a != b || !validVector(v, c.options.Dimensions) {
				return nil, ErrConflict
			}
			cache[keys[i]] = v
			continue
		}
		if readOnly {
			return nil, ErrConflict
		}
		seen[keys[i]] = true
		if len(text) > maxEmbeddingTextBytes {
			if !description {
				return nil, ErrEmbedding
			}
			v, e := c.embedLong(ctx, text)
			if e != nil {
				return nil, e
			}
			metas[i].Vector = v
			if e = c.put(ctx, keys[i], metas[i]); e != nil {
				return nil, e
			}
			cache[keys[i]] = v
			continue
		}
		missing = append(missing, i)
		pending = append(pending, text)
	}
	if len(pending) > 0 {
		vectors, e := c.embed(ctx, pending)
		if e != nil {
			return nil, e
		}
		for j, i := range missing {
			metas[i].Vector = slices.Clone(vectors[j])
			if e = c.put(ctx, keys[i], metas[i]); e != nil {
				return nil, e
			}
			cache[keys[i]] = vectors[j]
		}
	}
	for i := range texts {
		out[i] = slices.Clone(cache[keys[i]])
	}
	return out, nil
}

// Every source byte participates. UTF-8 chunks are bounded by provider input size;
// long-description chunk vectors are averaged, then L2 normalized once.
func (c *Client) embedLong(ctx context.Context, text string) ([]float32, error) {
	var chunks []string
	for len(text) > 0 {
		end := min(len(text), maxEmbeddingTextBytes)
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	mean := make([]float64, c.options.Dimensions)
	for start := 0; start < len(chunks); start += 64 {
		vectors, e := c.embed(ctx, chunks[start:min(start+64, len(chunks))])
		if e != nil {
			return nil, e
		}
		for _, v := range vectors {
			for i, n := range v {
				mean[i] += float64(n) / float64(len(chunks))
			}
		}
	}
	var norm float64
	for _, n := range mean {
		norm += n * n
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, ErrEmbedding
	}
	out := make([]float32, len(mean))
	for i, n := range mean {
		out[i] = float32(n / math.Sqrt(norm))
	}
	return out, nil
}
