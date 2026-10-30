// Package memeruntime composes a frozen Wiki corpus and query planner for both
// MASTER and next. It retrieves references and never sends or selects a reply.
package memeruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type Catalog interface {
	Active(context.Context) (memestore.GenerationManifest, error)
}
type KnowledgeCatalog interface {
	Knowledge(context.Context, memestore.GenerationManifest) (memes.KnowledgeSnapshot, error)
}
type Index interface {
	ForGeneration(memestore.GenerationManifest) (memes.Retriever, error)
}
type ModelFactory func(context.Context, string) (memes.Completion, error)
type Options struct {
	Visibility     memes.ReferenceVisibility
	Knowledge      KnowledgeCatalog
	KnowledgeFirst bool
	Scope          string
	Catalog        Catalog
	Index          Index
	Model          ModelFactory
	BotID          string
	AllowedChats   []string
	Reference      memes.ReferenceOptions
}
type Service struct {
	options      Options
	knowledgeMu  sync.Mutex
	knowledgeKey string
	knowledge    *memes.PreparedKnowledge
}

func New(o Options) (*Service, error) {
	if o.Catalog == nil || o.Index == nil || o.Model == nil || o.BotID == "" {
		return nil, errors.New("meme reference runtime dependencies missing")
	}
	if o.KnowledgeFirst && o.Knowledge == nil {
		return nil, errors.New("knowledge catalog required")
	}
	if o.Visibility == nil {
		policy, ok := o.Knowledge.(memes.ReferenceVisibility)
		if !ok && o.KnowledgeFirst {
			return nil, errors.New("current source visibility policy required")
		}
		if ok {
			o.Visibility = policy
		}
	}
	if o.Scope == "" {
		o.Scope = "wiki"
	}
	o.AllowedChats = append([]string(nil), o.AllowedChats...)
	return &Service{options: o}, nil
}

func failure(reason string) memes.ReferenceResult {
	return memes.ReferenceResult{Status: memes.ReferenceStatusError, Reason: reason, Cards: []memes.UseCard{}}
}
func (s *Service) References(ctx context.Context, chatID, messageID string, dialogue []memes.Turn) memes.ReferenceResult {
	if chatID == "" || messageID == "" || len(dialogue) == 0 {
		return failure("INVALID_HOST_CONTEXT")
	}
	if !s.chatEnabled(chatID) {
		return failure("CHAT_NOT_ENABLED")
	}

	if ctx.Err() != nil {
		return failure("TIMEOUT")
	}
	// Read once before planning; later activation cannot change this run's index.
	manifest, err := s.options.Catalog.Active(ctx)
	if err != nil {
		return failure("CORPUS_UNAVAILABLE")
	}
	retriever, err := s.options.Index.ForGeneration(manifest)
	indexUnavailable := err != nil
	if indexUnavailable && !s.options.KnowledgeFirst {
		return failure("CORPUS_INVALID")
	}
	if indexUnavailable {
		retriever = unavailableRetriever{}
	}
	model, err := s.options.Model(ctx, chatID)
	modelUnavailable := err != nil || model == nil
	if modelUnavailable && !s.options.KnowledgeFirst {
		return failure("MODEL_UNAVAILABLE")
	}
	if modelUnavailable {
		model = unavailableModel{}
	}
	options := s.options.Reference
	options.Metadata.CorpusHash = manifest.CorpusHash
	options.Metadata.ProjectionHash = manifest.ProjectionHash
	options.Metadata.RetrievalConfig, _ = json.Marshal(manifest)
	fingerprint, err := memes.Fingerprint([]any{s.options.BotID, chatID, messageID, dialogue})
	if err != nil {
		return failure("INVALID_HOST_CONTEXT")
	}
	request := memes.Request{RequestID: "meme-reference-" + fingerprint, Dialogue: dialogue, MaxChars: memes.DefaultMaxChars}
	deps := memes.ReferenceDependencies{Model: model, Retriever: retriever}
	if s.options.KnowledgeFirst {
		prepared, knowledgeErr := s.loadKnowledge(ctx, manifest)
		if knowledgeErr == nil {
			reference, e := memes.NewGroundedReferenceServiceWithKnowledge(deps, options, prepared)
			if e != nil {
				return failure("CONFIGURATION_INVALID")
			}
			result := reference.WithVisibility(s.options.Visibility).Retrieve(ctx, request)
			if indexUnavailable {
				result.Degraded = true
				result.Warnings = append(result.Warnings, memes.Warning{Code: "INDEX_UNAVAILABLE", Detail: "expansion index unavailable; verified priors preserved"})
			}
			return result
		}
		if modelUnavailable {
			return failure("MODEL_UNAVAILABLE")
		}
		if indexUnavailable {
			return failure("CORPUS_INVALID")
		}
		reference, e := memes.NewReferenceService(deps, options)
		if e != nil {
			return failure("CONFIGURATION_INVALID")
		}
		result := reference.Retrieve(ctx, request)
		result.Warnings = append(result.Warnings, memes.Warning{Code: "KNOWLEDGE_UNAVAILABLE", Detail: "generation-bound knowledge unavailable; compatible retrieval used"})
		result.Degraded = true
		return s.filterLegacyVisibility(ctx, result)
	}
	reference, err := memes.NewReferenceService(deps, options)
	if err != nil {
		return failure("CONFIGURATION_INVALID")
	}
	return s.filterLegacyVisibility(ctx, reference.Retrieve(ctx, request))
}

func (s *Service) filterLegacyVisibility(ctx context.Context, result memes.ReferenceResult) memes.ReferenceResult {
	if s.options.Visibility == nil || len(result.Cards) == 0 {
		return result
	}
	cards, err := s.options.Visibility.FilterUseCards(ctx, result.Cards)
	if err != nil {
		result.Cards = nil
		result.Status = memes.ReferenceStatusError
		result.Reason = "SOURCE_POLICY_UNAVAILABLE"
		result.Degraded = true
		return result
	}
	result.Cards = cards
	if len(cards) == 0 && result.Status == memes.ReferenceStatusReady {
		result.Status = memes.ReferenceStatusEmpty
		result.Reason = memes.ReasonNoCandidates
	}
	return result
}

type unavailableModel struct{}

func (unavailableModel) Complete(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
	return memes.CompletionResponse{}, errors.New("query model unavailable")
}

type unavailableRetriever struct{}

func (unavailableRetriever) Search(context.Context, memes.QueryPlan) (memes.RetrievalSnapshot, error) {
	return memes.RetrievalSnapshot{}, errors.New("expansion index unavailable")
}

func (s *Service) loadKnowledge(ctx context.Context, m memestore.GenerationManifest) (*memes.PreparedKnowledge, error) {
	key, _ := memes.Fingerprint(memestore.KnowledgeBinding(s.options.Scope, m))
	s.knowledgeMu.Lock()
	defer s.knowledgeMu.Unlock()
	if s.knowledgeKey == key && s.knowledge != nil {
		return s.knowledge, nil
	}
	snapshot, err := s.options.Knowledge.Knowledge(ctx, m)
	if err != nil {
		return nil, err
	}
	if snapshot.Binding != memestore.KnowledgeBinding(s.options.Scope, m) {
		return nil, errors.New("knowledge binding mismatch")
	}
	prepared, err := memes.PrepareKnowledge(snapshot)
	if err != nil {
		return nil, err
	}
	s.knowledgeKey = key
	s.knowledge = prepared
	return prepared, nil
}

// Warm loads immutable knowledge before accepting events. No model call occurs.
func (s *Service) Warm(ctx context.Context) error {
	if !s.options.KnowledgeFirst {
		return nil
	}
	m, err := s.options.Catalog.Active(ctx)
	if err != nil {
		return err
	}
	_, err = s.loadKnowledge(ctx, m)
	return err
}

func (s *Service) chatEnabled(chatID string) bool {
	if chatID == "" {
		return false
	}
	if len(s.options.AllowedChats) == 0 {
		return true
	}
	for _, id := range s.options.AllowedChats {
		if id == chatID {
			return true
		}
	}
	return false
}
