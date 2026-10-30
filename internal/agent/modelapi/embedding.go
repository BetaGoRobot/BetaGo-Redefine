package modelapi

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type Embedder struct {
	config EmbeddingConfig
	slots  chan struct{}
}

func NewEmbedder(c EmbeddingConfig) (*Embedder, error) {
	e, err := normalize(c.Endpoint)
	if err != nil {
		return nil, err
	}
	c.Endpoint = e
	if c.Protocol == "" {
		c.Protocol = "openai"
	}
	if c.Protocol != "openai" && c.Protocol != "ark_multimodal" {
		return nil, fmt.Errorf("%w: embedding protocol", domain.ErrInvalid)
	}
	if c.Dimensions < 1 || c.Dimensions > 65536 {
		return nil, fmt.Errorf("%w: embedding dimension", domain.ErrInvalid)
	}
	if c.Concurrency == 0 {
		c.Concurrency = 4
	}
	if c.MaxInputs == 0 {
		c.MaxInputs = 256
	}
	if c.Concurrency < 1 || c.Concurrency > 64 || c.MaxInputs < 1 || c.MaxInputs > 4096 {
		return nil, fmt.Errorf("%w: embedding batch", domain.ErrInvalid)
	}
	return &Embedder{config: c, slots: make(chan struct{}, c.Concurrency)}, nil
}
func validVector(v []float32, dim int) bool {
	if len(v) != dim {
		return false
	}
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c := e.config
	if len(texts) == 0 || len(texts) > c.MaxInputs {
		return nil, fmt.Errorf("%w: embedding input count", domain.ErrInvalid)
	}
	budget := int64(0)
	for _, s := range texts {
		budget += int64(len(s))
		if budget > c.MaxRequestBytes/6 {
			return nil, fmt.Errorf("%w: embedding input budget", domain.ErrInvalid)
		}
	}
	out := make([][]float32, len(texts))
	if c.Protocol == "openai" {
		var response struct {
			Data []struct {
				Index     *int      `json:"index"`
				Embedding []float32 `json:"embedding"`
			}
		}
		if err := e.call(ctx, "/embeddings", map[string]any{"model": c.Model, "input": texts, "dimensions": c.Dimensions, "encoding_format": "float"}, &response); err != nil {
			return nil, err
		}
		if len(response.Data) != len(texts) {
			return nil, fmt.Errorf("%w: embedding count", ErrProtocol)
		}
		seen := make([]bool, len(texts))
		for _, d := range response.Data {
			if d.Index == nil || *d.Index < 0 || *d.Index >= len(out) || seen[*d.Index] || !validVector(d.Embedding, c.Dimensions) {
				return nil, fmt.Errorf("%w: embedding index or vector", ErrProtocol)
			}
			seen[*d.Index] = true
			out[*d.Index] = d.Embedding
		}
		return out, nil
	}
	// Ark multimodal combines multiple input items into one vector. A bounded
	// worker pool calls it once per input, retaining the caller's original index.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	jobs := make(chan int)
	for range min(c.Concurrency, len(texts)) {
		wg.Go(func() {
			for i := range jobs {
				var response struct {
					Data struct {
						Embedding []float32 `json:"embedding"`
					} `json:"data"`
				}
				err := e.call(ctx, "/embeddings/multimodal", map[string]any{"model": c.Model, "dimensions": c.Dimensions, "input": []any{map[string]string{"type": "text", "text": texts[i]}}}, &response)
				if err == nil && !validVector(response.Data.Embedding, c.Dimensions) {
					err = fmt.Errorf("%w: embedding vector", ErrProtocol)
				}
				if err != nil {
					once.Do(func() { first = err; cancel() })
					continue
				}
				out[i] = response.Data.Embedding
			}
		})
	}
loop:
	for i := range texts {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return nil, first
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, nil
}

func (e *Embedder) call(ctx context.Context, path string, input, output any) error {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return request(ctx, e.config.Endpoint, path, input, output)
}
