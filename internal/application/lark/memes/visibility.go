package memes

import "context"

// ReferenceVisibility is evaluated against current authoritative state, not the
// warmed generation cache. Knowledge and retrieved uses share the same policy.
type ReferenceVisibility interface {
	CheckKnowledge(context.Context, KnowledgeSelection) (KnowledgeSelection, error)
	FilterUseCards(context.Context, []UseCard) ([]UseCard, error)
}

func KnowledgeSourceKey(source SourceMetadata) string {
	// Scope in SourceMetadata is descriptive provenance, not an ACL. The PG
	// catalog scope enforces visibility; withdrawal identity spans source versions.
	fp, _ := Fingerprint(struct{ Kind, URL, Title string }{source.Kind, source.URL, source.Title})
	return "source:" + fp
}

// FilterKnowledgeSelection keeps original observations even after a source is
// withdrawn. Its knowledge links only name entries that remain available.
func FilterKnowledgeSelection(selection KnowledgeSelection, allowed map[string]bool) KnowledgeSelection {
	out := KnowledgeSelection{Binding: selection.Binding, OmittedAnchorCount: selection.OmittedAnchorCount, Entries: []KnowledgeEntry{}, Anchors: []KnowledgeAnchor{}}
	for _, entry := range selection.Entries {
		if allowed[entry.ID] {
			out.Entries = append(out.Entries, entry)
		}
	}
	for _, anchor := range selection.Anchors {
		ids, matches := anchor.EntryIDs, anchor.EntryMatches
		anchor.EntryIDs = nil
		anchor.EntryMatches = nil
		for _, id := range ids {
			if allowed[id] {
				anchor.EntryIDs = append(anchor.EntryIDs, id)
			}
		}
		for _, match := range matches {
			if allowed[match.EntryID] {
				anchor.EntryMatches = append(anchor.EntryMatches, match)
			}
		}
		out.Anchors = append(out.Anchors, anchor)
	}
	return out
}

func RestrictGroundedContext(c *GroundedContext, current KnowledgeSelection) {
	allowed := map[string]bool{}
	for _, entry := range current.Entries {
		allowed[entry.ID] = true
	}
	priors := []GroundedPrior{}
	for _, entry := range c.PriorKnowledge {
		if allowed[entry.ID] {
			priors = append(priors, entry)
		}
	}
	if len(priors) < len(c.PriorKnowledge) {
		c.Stages.Knowledge = "withdrawn"
	}
	c.PriorKnowledge = priors
	provided := map[string]bool{}
	for _, entry := range priors {
		provided[entry.ID] = true
	}
	selection := FilterKnowledgeSelection(KnowledgeSelection{Anchors: c.Observations}, provided)
	c.Observations = selection.Anchors
	hypotheses := []GroundedHypothesis{}
	for _, h := range c.Interpretations {
		valid := true
		for _, id := range h.KnowledgeIDs {
			valid = valid && provided[id]
		}
		if valid {
			hypotheses = append(hypotheses, h)
		}
	}
	c.Interpretations = hypotheses
}
