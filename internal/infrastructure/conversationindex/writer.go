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
	return opensearch.UpsertData(ctx, index, documentID, payload)
}
