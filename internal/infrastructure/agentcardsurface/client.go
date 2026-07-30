package agentcardsurface

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

type ClientOptions struct {
	Suffix        string
	ReplyInThread bool
}

type Client struct {
	suffix        string
	replyInThread bool
}

var _ agentcard.SurfaceClient = (*Client)(nil)

func NewClient(options ClientOptions) *Client {
	suffix := options.Suffix
	if suffix == "" {
		suffix = "_agentCard"
	}
	return &Client{
		suffix: suffix, replyInThread: options.ReplyInThread,
	}
}

func (c *Client) ReplyCard(
	ctx context.Context,
	messageID string,
	card any,
) (string, error) {
	normalized, err := normalizeCard(card)
	if err != nil {
		return "", err
	}
	messageID, err = larkmsg.ReplyCardJSONReturning(
		ctx,
		messageID,
		normalized,
		c.suffix,
		c.replyInThread,
	)
	return messageID, classifyDeliveryError(err)
}

func (c *Client) CreateCard(
	ctx context.Context,
	chatID string,
	card any,
) (string, error) {
	normalized, err := normalizeCard(card)
	if err != nil {
		return "", err
	}
	messageID, err := larkmsg.CreateCardJSONReturning(
		ctx,
		chatID,
		normalized,
		createIdempotencyKey(normalized),
		c.suffix,
	)
	return messageID, classifyDeliveryError(err)
}

func createIdempotencyKey(card map[string]any) string {
	interactionID := findStringField(card, "interaction_id")
	if interactionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(interactionID))
	return "agent-card-" + hex.EncodeToString(sum[:16])
}

func findStringField(value any, field string) string {
	switch typed := value.(type) {
	case map[string]any:
		if result, ok := typed[field].(string); ok && result != "" {
			return result
		}
		for _, child := range typed {
			if result := findStringField(child, field); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range typed {
			if result := findStringField(child, field); result != "" {
				return result
			}
		}
	}
	return ""
}

func (c *Client) PatchCard(
	ctx context.Context,
	messageID string,
	card any,
) error {
	normalized, err := normalizeCard(card)
	if err != nil {
		return err
	}
	return larkmsg.PatchCardJSON(ctx, messageID, normalized)
}

func normalizeCard(card any) (map[string]any, error) {
	var encoded []byte
	var err error
	switch typed := card.(type) {
	case json.RawMessage:
		encoded = append([]byte(nil), typed...)
	case []byte:
		encoded = append([]byte(nil), typed...)
	default:
		encoded, err = json.Marshal(card)
		if err != nil {
			return nil, fmt.Errorf("marshal agent card surface: %w", err)
		}
	}
	var normalized map[string]any
	if !json.Valid(encoded) || json.Unmarshal(encoded, &normalized) != nil ||
		normalized == nil {
		return nil, errors.New("agent card surface must be a JSON object")
	}
	return normalized, nil
}

func classifyDeliveryError(err error) error {
	if err == nil {
		return nil
	}
	var netError net.Error
	if errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netError) && netError.Timeout()) {
		return fmt.Errorf("%w: transport timeout", agentcard.ErrSurfaceDeliveryAmbiguous)
	}
	return err
}
