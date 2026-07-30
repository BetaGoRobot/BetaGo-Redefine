package agentcardcapability

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type Executor struct {
	registry *tools.Impl[larkim.P2MessageReceiveV1]
}

func NewAgentCardCapabilityExecutor(
	registry *tools.Impl[larkim.P2MessageReceiveV1],
) (*Executor, error) {
	if registry == nil {
		return nil, errors.New("agent card capability registry is required")
	}
	return &Executor{registry: registry}, nil
}

func (e *Executor) Execute(
	ctx context.Context,
	invocation agentcard.CapabilityInvocation,
) (json.RawMessage, error) {
	if e == nil || e.registry == nil ||
		invocation.Version != "v1" ||
		strings.TrimSpace(invocation.Name) == "" ||
		invocation.Permission.Scope != invocation.Name ||
		strings.TrimSpace(invocation.Permission.ChatID) == "" ||
		strings.TrimSpace(invocation.Permission.ActorOpenID) == "" ||
		!strings.HasPrefix(invocation.IdempotencyKey, "cardcap_") ||
		!json.Valid(invocation.Input) ||
		!invocation.ResultPolicy.ContinueAgent ||
		invocation.ResultPolicy.SuccessSurfaceState != agentcard.SurfaceStatusResolved ||
		invocation.ResultPolicy.FailureSurfaceState != agentcard.SurfaceStatusFailed {
		return nil, errors.New("invalid agent card capability invocation")
	}
	switch invocation.ActorPolicy.Mode {
	case agentcard.ActorPolicyOwner:
		if invocation.ActorPolicy.OpenID == "" ||
			invocation.ActorPolicy.OpenID != invocation.Permission.ActorOpenID {
			return nil, errors.New("capability owner does not match permission actor")
		}
	case agentcard.ActorPolicyAnyMember:
		if invocation.ActorPolicy.OpenID != "" {
			return nil, errors.New("any-member capability cannot pin an owner")
		}
	default:
		return nil, errors.New("invalid capability actor policy")
	}
	unit, ok := e.registry.Get(invocation.Name)
	if !ok || unit == nil || unit.Function == nil {
		return nil, errors.New("capability is not registered")
	}
	ctx = runtimecontext.WithCapabilityExecutionIdentity(
		ctx,
		invocation.Name,
		invocation.IdempotencyKey,
		true,
	)
	result := unit.Function(
		ctx,
		string(invocation.Input),
		tools.FCMeta[larkim.P2MessageReceiveV1]{
			ChatID: invocation.Permission.ChatID,
			OpenID: invocation.Permission.ActorOpenID,
		},
	)
	if result.IsErr() {
		return nil, result.Err()
	}
	value := strings.TrimSpace(result.Value())
	if value == "" {
		return json.RawMessage(`{}`), nil
	}
	if json.Valid([]byte(value)) {
		return json.RawMessage(value), nil
	}
	encoded, err := json.Marshal(map[string]string{"text": value})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
