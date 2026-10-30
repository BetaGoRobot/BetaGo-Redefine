package atoms

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/presentation"
)

type MessageRetractor interface {
	Retract(context.Context, domain.Scope, string) error
}
type PresentationOptions struct {
	Messenger Messenger
	Retractor MessageRetractor
	Sources   domain.SourceReader
	Tasks     *pgstore.Store
}
type presentationInput struct {
	Card             presentation.Card `json:"card"`
	SourceMessageIDs []string          `json:"source_message_ids,omitempty"`
}

func presentationSchema() json.RawMessage {
	text := func(max int) any { return map[string]any{"type": "string", "maxLength": max} }
	section := catalogObject(map[string]any{"kind": catalogEnum("markdown", "table", "bar_chart"), "heading": text(128), "text": text(8192), "columns": map[string]any{"type": "array", "minItems": 1, "maxItems": 6, "items": text(64)}, "rows": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "array", "minItems": 1, "maxItems": 6, "items": text(256)}}, "bars": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": catalogObject(map[string]any{"label": catalogString(64), "value": map[string]any{"type": "number", "minimum": 0, "maximum": 1e15}}, "label", "value")}}, "kind")
	card := catalogObject(map[string]any{"title": catalogString(128), "theme": catalogEnum("blue", "green", "orange", "red", "grey"), "sections": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": section}, "links": map[string]any{"type": "array", "maxItems": 8, "items": catalogObject(map[string]any{"label": catalogString(128), "url": catalogString(4096)}, "label", "url")}}, "title", "sections")
	return catalogSchema(catalogObject(map[string]any{"card": card, "source_message_ids": map[string]any{"type": "array", "maxItems": 20, "uniqueItems": true, "items": catalogString(1024)}}, "card"))
}
func presentationSources(ctx context.Context, sources domain.SourceReader, scope domain.Scope, ids []string) ([]domain.SourceRef, error) {
	if scope.Validate() != nil || len(ids) > 20 {
		return nil, domain.ErrInvalid
	}
	refs := []domain.SourceRef{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if sources == nil {
			return nil, domain.ErrNotFound
		}
		m, err := sources.Message(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		if _, err = scopedMessage(m, scope, id); err != nil {
			return nil, err
		}
		if m.Deleted {
			return nil, domain.ErrStaleSource
		}
		refs = append(refs, m.Source)
	}
	if len(refs) > 0 {
		valid, err := sources.ValidSources(ctx, domain.Query{Scope: scope, Anchor: time.Now().UTC(), Mode: domain.QueryLiteral, Limit: len(refs)}, refs)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, domain.ErrStaleSource
		}
	}
	return refs, nil
}
func presentationGuard(ctx context.Context, store *pgstore.Store, in Invocation, name, args string, automatic bool) error {
	if store == nil {
		return nil
	}
	if err := store.CheckTask(ctx, in.Job, in.Task); err != nil {
		return err
	}
	if err := store.CheckToolPolicy(ctx, name, in.PolicyDigest, true, automatic); err != nil {
		return err
	}
	x, err := store.ToolExecution(ctx, in.Task.Scope, in.Task.ID, in.ExecutionKey)
	if err != nil {
		return err
	}
	if x.Scope != in.Task.Scope || x.TaskID != in.Task.ID || x.Tool != name || x.Status != "running_effect" || x.JobID != in.Job.ID || x.Fence != in.Job.Fence || x.Revision != in.Task.Revision || x.ActorID == "" || !domain.SameArguments(x.Arguments, args) {
		return domain.ErrUnauthorized
	}
	if automatic && x.ApprovalID != "" {
		return domain.ErrUnauthorized
	}
	if !automatic && x.ApprovalID == "" {
		return domain.ErrUnauthorized
	}
	return nil
}
func PresentationDefinitions(o PresentationOptions) []Definition {
	compose := func(ctx context.Context, in Invocation, args string) (presentationInput, []byte, []domain.SourceRef, error) {
		var a presentationInput
		if err := catalogDecode(args, &a); err != nil {
			return a, nil, nil, err
		}
		refs, err := presentationSources(ctx, o.Sources, in.Task.Scope, a.SourceMessageIDs)
		if err != nil {
			return a, nil, nil, err
		}
		raw, err := presentation.Compose(a.Card)
		return a, raw, refs, err
	}
	out := []Definition{
		{Name: "discover_card_components", Description: "Discover the typed display-only card vocabulary and bounds. Composition supports Markdown text, labeled table rows, text bar charts and separately validated public HTTPS links. It never adds callbacks, actions, mentions or arbitrary card JSON.", Run: func(ctx context.Context, _ Invocation, args string) (string, error) {
			var a struct{}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			return encodeCommunity(map[string]any{"schema": "2.0", "components": []string{"markdown", "table", "bar_chart"}, "max_sections": 12, "max_table_columns": 6, "max_table_rows": 20, "max_chart_bars": 12, "max_links": 8, "max_payload_bytes": 24 << 10, "table_rendering": "labeled Markdown rows", "chart_rendering": "scaled text bars with exact numeric values"})
		}},
		{Name: "compose_card", Description: "Compose a bounded typed display card without sending. source_message_ids are resolved to current authoritative group originals; source content never grants approval. Returns provider payload and actual sources; pass the same typed card to feishu_send_card to deliver.", Parameters: presentationSchema(), sourceRefs: catalogSources, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			_, raw, refs, err := compose(ctx, in, args)
			if err != nil {
				return "", err
			}
			return encodeCommunity(map[string]any{"card": json.RawMessage(raw), "sources": refs, "sent": false})
		}},
	}
	if o.Messenger != nil {
		out = append(out, Definition{Name: "feishu_send_card", Description: "Send the requested typed display card in this task's group, replying to the current task. Ordinary same-group communication; stable execution UUID prevents replay. No callbacks/actions/raw JSON/arbitrary mentions. All supplied source_message_ids must still be current local originals.", Parameters: presentationSchema(), SideEffect: true, AutoExecute: true, sourceRefs: catalogSources, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			_, raw, refs, err := compose(ctx, in, args)
			if err != nil {
				return "", err
			}
			if err = presentationGuard(ctx, o.Tasks, in, "feishu_send_card", args, true); err != nil {
				return "", err
			}
			if o.Tasks != nil && len(refs) > 0 {
				if err = o.Tasks.AddTurnSources(ctx, in.Job, in.Task, refs); err != nil {
					return "", err
				}
			}
			id, err := o.Messenger.Send(ctx, in.Task.Scope, in.Task.Root.MessageID, "interactive", string(raw), domain.StableUUID(in.Task.Scope.Key(), in.Task.ID, in.ExecutionKey))
			if err != nil {
				return "", err
			}
			if id == "" {
				return "", domain.ErrInvalid
			}
			return encodeCommunity(map[string]any{"message_id": id, "sources": refs, "sent": true})
		}})
	}
	if o.Retractor != nil {
		out = append(out, Definition{Name: "feishu_retract_message", Description: "Retract one actual message sent by this application/bot in the current group. Provider sender and exact group are rechecked; user messages and other applications' messages are rejected. Requires explicit approval.", Parameters: messageSchema, SideEffect: true, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct {
				MessageID string `json:"message_id"`
			}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			if a.MessageID == "" {
				return "", domain.ErrInvalid
			}
			if err := presentationGuard(ctx, o.Tasks, in, "feishu_retract_message", args, false); err != nil {
				return "", err
			}
			if err := o.Retractor.Retract(ctx, in.Task.Scope, a.MessageID); err != nil {
				return "", err
			}
			return encodeCommunity(map[string]any{"message_id": a.MessageID, "retracted": true})
		}})
	}
	return out
}
