package atoms

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// Keep complete observations below the native receipt ceiling, with room for
// surrounding model context. Count JSON bytes, including escaping and proof.
const communityResultBytes = 64 << 10
const communityTurnSources = 256

func communityBudgetUnavailable(reason string) (string, error) {
	return encodeCommunity(communityResult{Available: false, Reason: reason, Truncated: true, Hint: "Request a narrower read or attachment sequence. No continuation page was advanced."})
}
func dedupCommunityRefs(refs []domain.SourceRef) []domain.SourceRef {
	seen := map[domain.SourceRef]bool{}
	out := make([]domain.SourceRef, 0, len(refs))
	for _, ref := range refs {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}
func communityFits(data any, refs []domain.SourceRef) bool {
	b, e := json.Marshal(communityResult{Available: true, Data: data, Sources: dedupCommunityRefs(refs)})
	return e == nil && len(b) <= communityResultBytes
}

type communitySourceBudget struct{ seen map[domain.SourceRef]bool }

func communitySourcesBudget(ctx context.Context, o Options, in Invocation) (communitySourceBudget, error) {
	refs := in.Task.ContextSources
	// boundTool keeps its original Task value through a native tool loop. Read
	// the current ledger so evidence admitted by earlier calls consumes slots.
	if o.Tasks != nil && in.Task.ID != "" {
		current, e := o.Tasks.Task(ctx, in.Task.Scope, in.Task.ID)
		if e != nil {
			return communitySourceBudget{}, e
		}
		refs = current.ContextSources
	}
	b := communitySourceBudget{seen: map[domain.SourceRef]bool{}}
	for _, ref := range refs {
		b.seen[ref] = true
	}
	return b, nil
}
func (b communitySourceBudget) with(refs []domain.SourceRef) (communitySourceBudget, bool) {
	next := communitySourceBudget{seen: make(map[domain.SourceRef]bool, len(b.seen)+len(refs))}
	for ref := range b.seen {
		next.seen[ref] = true
	}
	for _, ref := range refs {
		next.seen[ref] = true
	}
	return next, len(next.seen) <= communityTurnSources
}
func guardCommunityRead(ctx context.Context, o Options, in Invocation, raw string, extract func(string) ([]domain.SourceRef, error)) (string, error) {
	if len(raw) > communityResultBytes {
		return communityBudgetUnavailable("result_budget_exceeded")
	}
	refs, e := extract(raw)
	if e != nil {
		return "", e
	}
	if len(refs) == 0 {
		return raw, nil
	}
	budget, e := communitySourcesBudget(ctx, o, in)
	if e != nil {
		return communityFailure(ctx, e)
	}
	if _, ok := budget.with(refs); !ok {
		return communityBudgetUnavailable("source_budget_exceeded")
	}
	return raw, nil
}

type mediaView struct {
	domain.Media
	MetadataTruncated bool `json:"metadata_truncated,omitempty"`
}

func communityMediaPreview(media []domain.Media) ([]mediaView, bool, *int) {
	out := []mediaView{}
	bytesLeft := 8 << 10
	truncated := false
	var next *int
	for i, v := range media {
		if len(out) >= 16 {
			n := v.Sequence
			next = &n
			truncated = true
			break
		}
		p := mediaView{Media: v}
		p.MIME = cutCommunity(v.MIME, 128)
		p.FileName = cutCommunity(v.FileName, 256)
		p.MetadataTruncated = p.MIME != v.MIME || p.FileName != v.FileName
		encoded, e := json.Marshal(p)
		if e != nil || len(encoded) > bytesLeft {
			n := v.Sequence
			next = &n
			truncated = true
			break
		}
		bytesLeft -= len(encoded)
		out = append(out, p)
		truncated = truncated || p.MetadataTruncated
		if i == len(media)-1 {
			next = nil
		}
	}
	return out, truncated, next
}
