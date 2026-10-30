package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
)

func (s *Service) recoverContext(ctx context.Context, j domain.Job, cause error) (bool, error) {
	if j.Kind != domain.JobAgentTurn || !errors.Is(cause, domain.ErrStaleSource) {
		return false, nil
	}
	var request domain.TurnRequest
	if json.Unmarshal(j.Payload, &request) != nil {
		return false, domain.ErrInvalid
	}
	return s.c.Store.ReplanStaleContext(ctx, j, request)
}

// Safe categories are useful in runtime logs without echoing model/vendor
// errors, message text, URLs, credentials, or personal identity identifiers.
func rejectionReason(err error) string {
	for _, v := range []struct {
		e    error
		name string
	}{{context.DeadlineExceeded, "deadline"}, {context.Canceled, "canceled"}, {modelapi.ErrProtocol, "model_protocol"}, {engine.ErrConversationOutput, "model_output"}, {domain.ErrNotFound, "not_found"}, {domain.ErrUnauthorized, "unauthorized"}, {domain.ErrInvalid, "invalid"}, {domain.ErrConflict, "conflict"}, {domain.ErrStaleSource, "source_changed"}, {domain.ErrEffectUnknown, "effect_unknown"}} {
		if errors.Is(err, v.e) {
			return v.name
		}
	}
	return "operation_failed"
}
