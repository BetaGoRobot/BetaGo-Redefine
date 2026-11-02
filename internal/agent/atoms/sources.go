package atoms

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

func retrievalSources(raw string) ([]domain.SourceRef, error) {
	var r retrieval.Result
	if e := json.Unmarshal([]byte(raw), &r); e != nil {
		return nil, e
	}
	var refs []domain.SourceRef
	for _, ev := range r.Evidence {
		refs = append(refs, ev.Sources...)
	}
	return refs, nil
}
func messageSources(raw string) ([]domain.SourceRef, error) {
	var availability struct {
		Available *bool `json:"available"`
	}
	if json.Unmarshal([]byte(raw), &availability) != nil {
		return nil, domain.ErrInvalid
	}
	if availability.Available != nil && !*availability.Available {
		return nil, nil
	}
	var m domain.Message
	if e := json.Unmarshal([]byte(raw), &m); e != nil {
		return nil, e
	}
	if e := m.Source.Validate(); e != nil {
		return nil, e
	}
	return []domain.SourceRef{m.Source}, nil
}
func (d Definition) Sources(raw string) ([]domain.SourceRef, error) {
	if d.sourceRefs == nil {
		return nil, nil
	}
	return d.sourceRefs(raw)
}

type messageReader interface {
	ReadMessage(context.Context, domain.Scope, string) (domain.Message, error)
}

func currentMessage(ctx context.Context, o Options, scope domain.Scope, id string) (domain.Message, error) {
	if e := ctx.Err(); e != nil {
		return domain.Message{}, e
	}
	if scope.Validate() != nil || id == "" {
		return domain.Message{}, domain.ErrInvalid
	}
	if o.Sources == nil {
		return domain.Message{}, domain.ErrNotFound
	}
	m, err := o.Sources.Message(ctx, scope, id)
	if err == nil {
		return scopedMessage(m, scope, id)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Message{}, err
	}
	var remote messageReader = o.Group
	if remote == nil {
		remote, _ = o.Messenger.(messageReader)
	}
	if remote == nil {
		return m, err
	}
	latest, e := remote.ReadMessage(ctx, scope, id)
	if e != nil {
		return domain.Message{}, e
	}
	if latest, e = scopedMessage(latest, scope, id); e != nil {
		return domain.Message{}, e
	}
	if o.Tasks == nil {
		return latest, nil
	}
	kind := domain.EventMessage
	if latest.Deleted {
		kind = domain.EventDelete
	}
	if _, e = o.Tasks.Accept(ctx, domain.Event{ID: latest.EventID, Type: kind, Message: latest, OccurredAt: latest.UpdatedAt}); e != nil {
		return domain.Message{}, e
	}
	m, e = o.Sources.Message(ctx, scope, id)
	if e != nil {
		return domain.Message{}, e
	}
	return scopedMessage(m, scope, id)
}

func scopedMessage(m domain.Message, scope domain.Scope, id string) (domain.Message, error) {
	if m.Source.Scope != scope || m.Source.MessageID != id {
		return domain.Message{}, domain.ErrUnauthorized
	}
	if m.Source.Version <= 0 || (!m.Deleted && m.Source.Validate() != nil) {
		return domain.Message{}, domain.ErrInvalid
	}
	return m, nil
}
