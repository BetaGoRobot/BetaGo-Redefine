package conversationindex

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
)

var _ agentruntime.ProjectionWriter = OpenSearchWriter{}

type OpenSearchWriter struct{}

func (OpenSearchWriter) Upsert(
	ctx context.Context,
	index string,
	documentID string,
	payload json.RawMessage,
) error {
	return safeOpenSearchError(opensearch.UpsertData(ctx, index, documentID, payload))
}

type redactedOpenSearchError struct {
	cause error
}

func safeOpenSearchError(err error) error {
	if err == nil {
		return nil
	}
	return redactedOpenSearchError{cause: err}
}

func (redactedOpenSearchError) Error() string {
	return "conversation projection index write failed"
}

func (e redactedOpenSearchError) Unwrap() error {
	return e.cause
}
