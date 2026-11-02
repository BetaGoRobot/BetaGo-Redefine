package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"regexp"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// Group preferences are source-bound material, never system policy or authority.
// A selected reply is a suggestion for the model's normal conversation decision;
// it does not cause an extra untracked send alongside the durable final answer.
type groupPreferenceView struct {
	instructionSource *domain.SourceRef
	Instruction       string
	Corrections       []domain.CatalogCorrection
	SelectedReply     *domain.CatalogReplyRule `json:",omitempty"`
	SelectedPhrase    *domain.CatalogPhrase    `json:",omitempty"`
	Untrusted         bool
	Truncated         bool
}

func (e *Engine) groupPreferences(ctx context.Context, t domain.Task) (*groupPreferenceView, []domain.SourceRef, error) {
	r, err := e.c.Store.CatalogRuntime(ctx, t.Scope)
	if err != nil {
		return nil, nil, err
	}
	v := &groupPreferenceView{Instruction: r.ReplyInstruction, instructionSource: r.ReplyInstructionSource, Untrusted: true, Corrections: []domain.CatalogCorrection{}}
	refs := []domain.SourceRef{}
	if v.Instruction != "" && r.ReplyInstructionSource != nil {
		refs = append(refs, *r.ReplyInstructionSource)
	}
	for _, c := range r.Corrections {
		if len(v.Corrections) == 16 {
			v.Truncated = true
			break
		}
		v.Corrections = append(v.Corrections, c)
		refs = append(refs, c.Origin)
	}
	var selected []domain.CatalogReplyRule
	weight := 0
	for _, rule := range r.Rules {
		matches := false
		switch rule.Match {
		case "exact":
			matches = t.Request == rule.Pattern
		case "contains":
			matches = strings.Contains(t.Request, rule.Pattern)
		case "prefix":
			matches = strings.HasPrefix(t.Request, rule.Pattern)
		case "regex":
			matches, _ = regexp.MatchString(rule.Pattern, t.Request)
		}
		seed := sha256.Sum256([]byte(domain.StableID(t.Scope.Key(), t.Root.MessageID, rule.Name)))
		chance := float64(binary.BigEndian.Uint64(seed[:8])>>11) / float64(uint64(1)<<53)
		if matches && rule.Enabled && chance < rule.Probability {
			selected = append(selected, rule)
			weight += rule.Weight
		}
	}
	if weight > 0 {
		seed := sha256.Sum256([]byte(domain.StableID(t.Scope.Key(), t.Root.MessageID, "reply-selection")))
		pick := int(binary.BigEndian.Uint64(seed[:8]) % uint64(weight))
		for _, rule := range selected {
			pick -= rule.Weight
			if pick < 0 {
				copy := rule
				v.SelectedReply = &copy
				refs = append(refs, rule.Origin)
				if rule.AssetSource != nil {
					refs = append(refs, *rule.AssetSource)
				}
				break
			}
		}
	}
	if v.SelectedReply == nil && len(r.Phrases) > 0 {
		total := 0
		for _, phrase := range r.Phrases {
			if phrase.Enabled {
				total += phrase.Weight
			}
		}
		if total > 0 {
			seed := sha256.Sum256([]byte(domain.StableID(t.Scope.Key(), t.Root.MessageID, "phrase-selection")))
			pick := int(binary.BigEndian.Uint64(seed[:8]) % uint64(total))
			for _, phrase := range r.Phrases {
				if !phrase.Enabled {
					continue
				}
				pick -= phrase.Weight
				if pick < 0 {
					copy := phrase
					v.SelectedPhrase = &copy
					refs = append(refs, phrase.Origin)
					break
				}
			}
		}
	}
	if v.Instruction == "" && len(v.Corrections) == 0 && v.SelectedReply == nil && v.SelectedPhrase == nil {
		return nil, nil, nil
	}
	return v, refs, nil
}

func preferenceSources(v *groupPreferenceView) []domain.SourceRef {
	if v == nil {
		return nil
	}
	var refs []domain.SourceRef
	if v.Instruction != "" && v.instructionSource != nil {
		refs = append(refs, *v.instructionSource)
	}
	for _, c := range v.Corrections {
		refs = append(refs, c.Origin)
	}
	if r := v.SelectedReply; r != nil {
		refs = append(refs, r.Origin)
		if r.AssetSource != nil {
			refs = append(refs, *r.AssetSource)
		}
	}
	if v.SelectedPhrase != nil {
		refs = append(refs, v.SelectedPhrase.Origin)
	}
	return refs
}
func trimGroupPreferences(v *groupPreferenceView) bool {
	if v == nil {
		return false
	}
	switch {
	case len(v.Corrections) > 0:
		v.Corrections = v.Corrections[:len(v.Corrections)-1]
	case v.SelectedPhrase != nil:
		v.SelectedPhrase = nil
	case v.SelectedReply != nil:
		v.SelectedReply = nil
	case v.Instruction != "":
		v.Instruction = ""
	default:
		return false
	}
	v.Truncated = true
	return true
}
