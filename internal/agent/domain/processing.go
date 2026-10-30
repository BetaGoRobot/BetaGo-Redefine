package domain

import (
	"context"
	"time"
)

type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}
type Reranker interface {
	Rerank(context.Context, string, []Evidence) ([]Evidence, error)
}

const JobEmbedContext JobKind = "embed_context"

type Object struct {
	Key, MIME, ETag string
	Size            int64
	Source          SourceRef
	ResourceID      string
	Sequence        int
}
type MediaPart struct {
	Object     Object
	Text       string
	Page       int
	Start, End time.Duration
}
type MediaArtifact struct {
	Source SourceRef
	Parts  []MediaPart
}
type PipelineStage struct {
	Source         SourceRef
	Name           string
	ContextID      string
	ContextVersion int64
	CompletedAt    time.Time
}
