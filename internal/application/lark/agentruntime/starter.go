package agentruntime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const scheduleEditInteractionKind = "schedule_edit"

// CreateScheduleEditInteractionRequest is the application-layer contract for
// atomically creating a shadow run and its first durable wait. Implementations
// must make replay by Run.TriggerMessageID idempotent and must not replace an
// unrelated active run.
type CreateScheduleEditInteractionRequest struct {
	Run           StartRunRequest
	StepID        string
	InteractionID string
	TokenHash     string
	TrustedInput  json.RawMessage
	WaitTTL       time.Duration
	Projection    ProjectionDocument
}

func (r CreateScheduleEditInteractionRequest) Validate() error {
	if err := validateCanonical("source_message_id", r.Run.TriggerMessageID); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "app_id", value: r.Run.AppID},
		{name: "bot_open_id", value: r.Run.BotOpenID},
		{name: "chat_id", value: r.Run.ChatID},
		{name: "actor_open_id", value: r.Run.ActorOpenID},
		{name: "step_id", value: r.StepID},
		{name: "interaction_id", value: r.InteractionID},
	} {
		if err := validateCanonical(field.name, field.value); err != nil {
			return err
		}
	}
	tokenHash, err := hex.DecodeString(r.TokenHash)
	if err != nil || len(tokenHash) != sha256.Size {
		return invalidRuntimeContract("token_hash must be a SHA-256 hex digest")
	}
	if r.WaitTTL <= 0 {
		return invalidRuntimeContract("wait_ttl must be positive")
	}
	if _, err := DecodeScheduleEditTrustedInput(r.TrustedInput); err != nil {
		return err
	}
	return r.Projection.Validate()
}

type StartScheduleEditInteractionResult struct {
	RunID         string
	StepID        string
	InteractionID string
	Revision      int64
	ExpiresAt     time.Time
}

func (r StartScheduleEditInteractionResult) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "run_id", value: r.RunID},
		{name: "step_id", value: r.StepID},
		{name: "interaction_id", value: r.InteractionID},
	} {
		if err := validateCanonical(field.name, field.value); err != nil {
			return err
		}
	}
	if r.Revision <= 0 {
		return invalidRuntimeContract("revision must be positive")
	}
	if r.ExpiresAt.IsZero() {
		return invalidRuntimeContract("expires_at is required")
	}
	return nil
}

type ScheduleEditInteractionCreator interface {
	CreateScheduleEditInteraction(
		context.Context,
		CreateScheduleEditInteractionRequest,
	) (StartScheduleEditInteractionResult, error)
}

type DurableScheduleEditStarterOptions struct {
	Store           ScheduleEditInteractionCreator
	AppID           string
	BotOpenID       string
	TokenSecret     []byte
	WaitTTL         time.Duration
	ProjectionIndex string
}

type DurableScheduleEditStarter struct {
	store           ScheduleEditInteractionCreator
	appID           string
	botOpenID       string
	tokenSecret     []byte
	waitTTL         time.Duration
	projectionIndex string
}

func NewDurableScheduleEditStarter(opts DurableScheduleEditStarterOptions) (*DurableScheduleEditStarter, error) {
	if isNilRuntimeDependency(opts.Store) {
		return nil, errors.New("schedule edit interaction store is nil")
	}
	for name, value := range map[string]string{
		"app_id": opts.AppID, "bot_open_id": opts.BotOpenID, "projection_index": opts.ProjectionIndex,
	} {
		if err := validateCanonical(name, value); err != nil {
			return nil, err
		}
	}
	if len(opts.TokenSecret) == 0 {
		return nil, errors.New("schedule edit interaction token secret is empty")
	}
	if opts.WaitTTL <= 0 {
		return nil, errors.New("schedule edit interaction wait TTL must be positive")
	}
	return &DurableScheduleEditStarter{
		store:           opts.Store,
		appID:           opts.AppID,
		botOpenID:       opts.BotOpenID,
		tokenSecret:     append([]byte(nil), opts.TokenSecret...),
		waitTTL:         opts.WaitTTL,
		projectionIndex: opts.ProjectionIndex,
	}, nil
}

func (s *DurableScheduleEditStarter) StartScheduleEdit(
	ctx context.Context,
	req StartScheduleEditRequest,
) (*RuntimeEnvelope, error) {
	if s == nil || isNilRuntimeDependency(s.store) {
		return nil, errors.New("durable schedule edit starter is not configured")
	}
	if err := validateCanonical("source_message_id", req.SourceMessageID); err != nil {
		return nil, err
	}
	trusted, err := EncodeScheduleEditTrustedInput(req)
	if err != nil {
		return nil, err
	}
	digest := scheduleEditStartDigest(s.appID, s.botOpenID, trusted)
	interactionID := "interaction_schedule_" + digest
	stepID := "step_wait_" + digest
	token := s.interactionToken(interactionID)
	projectionPayload, err := json.Marshal(struct {
		SchemaVersion   int    `json:"schema_version"`
		Type            string `json:"type"`
		ChatID          string `json:"chat_id"`
		ActorOpenID     string `json:"actor_open_id"`
		SourceMessageID string `json:"source_message_id"`
		InteractionID   string `json:"interaction_id"`
	}{
		SchemaVersion:   1,
		Type:            "schedule_edit_wait",
		ChatID:          req.ChatID,
		ActorOpenID:     req.ActorOpenID,
		SourceMessageID: req.SourceMessageID,
		InteractionID:   interactionID,
	})
	if err != nil {
		return nil, err
	}
	result, err := s.store.CreateScheduleEditInteraction(ctx, CreateScheduleEditInteractionRequest{
		Run: StartRunRequest{
			AppID:            s.appID,
			BotOpenID:        s.botOpenID,
			ChatID:           req.ChatID,
			ScopeType:        ScopeTypeChat,
			ScopeID:          req.ChatID,
			TriggerType:      TriggerTypeShadow,
			TriggerMessageID: req.SourceMessageID,
			ActorOpenID:      req.ActorOpenID,
			Goal:             fmt.Sprintf("Confirm schedule edit for task %s", req.TaskID),
		},
		StepID:        stepID,
		InteractionID: interactionID,
		TokenHash:     HashInteractionToken(token),
		TrustedInput:  trusted,
		WaitTTL:       s.waitTTL,
		Projection: ProjectionDocument{
			IndexAlias: s.projectionIndex,
			DocumentID: interactionID + ":wait",
			Payload:    projectionPayload,
		},
	})
	if err != nil {
		return nil, err
	}
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("invalid persisted schedule edit interaction: %w", err)
	}
	envelope := &RuntimeEnvelope{
		RunID:           result.RunID,
		StepID:          result.StepID,
		InteractionID:   result.InteractionID,
		Revision:        result.Revision,
		Token:           token,
		InteractionKind: scheduleEditInteractionKind,
		ContinueAgent:   true,
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	return envelope, nil
}

func scheduleEditStartDigest(appID, botOpenID string, trusted json.RawMessage) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{appID, botOpenID, string(trusted)}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func (s *DurableScheduleEditStarter) interactionToken(interactionID string) string {
	mac := hmac.New(sha256.New, s.tokenSecret)
	_, _ = mac.Write([]byte("schedule-edit\x00"))
	_, _ = mac.Write([]byte(interactionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
