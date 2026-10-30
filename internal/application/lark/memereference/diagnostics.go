package memereference

import "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"

// GroundedDiagnostics retains routing/provenance evidence without logging raw
// conversation, source bodies, model text, transport errors or credentials.
func GroundedDiagnostics(result memes.ReferenceResult) any {
	c := result.ContextV2
	if c == nil {
		return nil
	}
	type decision struct {
		Index        int         `json:"index"`
		Origin       string      `json:"origin"`
		Field        memes.Field `json:"field"`
		Accepted     bool        `json:"accepted"`
		Reason       string      `json:"reason,omitempty"`
		SourceIDs    []int       `json:"source_ids,omitempty"`
		AnchorIDs    []string    `json:"anchor_ids,omitempty"`
		KnowledgeIDs []string    `json:"knowledge_ids,omitempty"`
	}
	decisions := []decision{}
	stop := ""
	if trace := result.Plan.Grounded; trace != nil {
		switch trace.StopReason {
		case "serious_question", "sufficient_knowledge", "no_useful_question", "budget_exhausted", "no_anchor":
			stop = trace.StopReason
		default:
			if trace.StopReason != "" {
				stop = "other"
			}
		}
		for _, q := range trace.Decisions {
			item := decision{Index: q.Index, Origin: q.Proposal.Origin, Field: q.Proposal.Field, Accepted: q.Accepted, Reason: q.Reason}
			switch item.Origin {
			case "literal", "knowledge_link", "hypothesis_probe":
			default:
				item.Origin = "invalid"
			}
			switch item.Field {
			case memes.FieldKeys, memes.FieldRelation, memes.FieldEmotion, memes.FieldDescription:
			default:
				item.Field = "invalid"
			}
			if q.Accepted {
				item.SourceIDs = q.Proposal.SourceIDs
				item.AnchorIDs = q.Proposal.AnchorIDs
				item.KnowledgeIDs = q.Proposal.KnowledgeIDs
			}
			decisions = append(decisions, item)
		}
		for _, rejected := range trace.RejectedInputs {
			if rejected.Kind == "query" {
				decisions = append(decisions, decision{Index: rejected.Index, Origin: "invalid", Field: "invalid", Reason: "INVALID_QUERY_SCHEMA"})
			}
		}
	}
	return struct {
		Version              int                  `json:"version"`
		Generation           string               `json:"generation"`
		KnowledgeFingerprint string               `json:"knowledge_fingerprint"`
		KnowledgeMS          int64                `json:"knowledge_ms"`
		Anchors              int                  `json:"anchors"`
		Priors               int                  `json:"priors"`
		OmittedAnchors       int                  `json:"omitted_anchors"`
		OmittedDetails       int                  `json:"omitted_details"`
		Stages               memes.GroundedStages `json:"stages"`
		StopReason           string               `json:"stop_reason"`
		Queries              []decision           `json:"queries"`
	}{c.Version, c.Binding.Generation, c.KnowledgeFingerprint, c.KnowledgeMS, len(c.Observations), len(c.PriorKnowledge), c.OmittedAnchors, c.OmittedKnowledgeDetails, c.Stages, stop, decisions}
}
