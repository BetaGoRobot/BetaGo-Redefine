package memeruntime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

// Revalidate checks restored/continued environments without invoking a planner,
// changing their frozen corpus generation or performing any business action.
func (s *Service) Revalidate(ctx context.Context, chatID string, c *memes.GroundedContext, cards []memes.UseCard) (*memes.GroundedContext, []memes.UseCard, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if !s.chatEnabled(chatID) {
		return nil, nil, errors.New("meme references not enabled for this chat")
	}
	if s.options.Visibility == nil {
		return c, cards, nil
	}
	filtered, err := s.options.Visibility.FilterUseCards(ctx, cards)
	if err != nil {
		return nil, nil, errors.New("current source visibility unavailable")
	}
	if c == nil {
		return nil, filtered, nil
	}
	if !s.options.KnowledgeFirst {
		return nil, filtered, nil
	}
	if c.Binding.Scope != s.options.Scope {
		return nil, nil, errors.New("knowledge environment scope mismatch")
	}
	validator, ok := s.options.Knowledge.(interface {
		ValidateKnowledgeEnvironment(context.Context, *memes.GroundedContext) error
	})
	if !ok || validator.ValidateKnowledgeEnvironment(ctx, c) != nil {
		return nil, nil, errors.New("knowledge environment binding invalid")
	}
	selection := memes.KnowledgeSelection{Binding: c.Binding, Anchors: c.Observations}
	for _, p := range c.PriorKnowledge {
		selection.Entries = append(selection.Entries, memes.KnowledgeEntry{ID: p.ID, Binding: p.Binding, Sources: p.Sources, RecordFingerprints: p.RecordFingerprints})
	}
	current, err := s.options.Visibility.CheckKnowledge(ctx, selection)
	if err != nil {
		return nil, nil, errors.New("current source visibility unavailable")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, nil, errors.New("knowledge environment invalid")
	}
	var detached memes.GroundedContext
	if json.Unmarshal(raw, &detached) != nil {
		return nil, nil, errors.New("knowledge environment invalid")
	}
	memes.RestrictGroundedContext(&detached, current)
	return &detached, filtered, nil
}
