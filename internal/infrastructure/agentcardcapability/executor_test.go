package agentcardcapability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestAgentCardCapabilityExecutorUsesPermissionBoundToolMetadata(t *testing.T) {
	registry := toolkit.New[larkim.P2MessageReceiveV1]()
	var gotArgs string
	var gotMeta toolkit.FCMeta[larkim.P2MessageReceiveV1]
	var gotCapabilityName string
	var gotIdempotencyKey string
	registry.Add(
		toolkit.NewUnit[larkim.P2MessageReceiveV1]().
			Name("schedule.update").
			Func(func(
				ctx context.Context,
				args string,
				meta toolkit.FCMeta[larkim.P2MessageReceiveV1],
			) gresult.R[string] {
				gotArgs = args
				gotMeta = meta
				gotCapabilityName = runtimecontext.CapabilityExecutionName(ctx)
				gotIdempotencyKey =
					runtimecontext.CapabilityExecutionIdempotencyKey(ctx)
				return gresult.OK(`{"updated":true}`)
			}),
	)
	executor, err := NewAgentCardCapabilityExecutor(registry)
	if err != nil {
		t.Fatal(err)
	}
	output, err := executor.Execute(
		context.Background(),
		agentcard.CapabilityInvocation{
			Name: "schedule.update", Version: "v1",
			Input:          json.RawMessage(`{"task_id":"trusted"}`),
			IdempotencyKey: "cardcap_123",
			Permission: agentcard.CapabilityPermissionContext{
				Scope: "schedule.update", ChatID: "chat-1",
				ActorOpenID: "owner-1",
			},
			ActorPolicy: agentcard.ActorPolicy{
				Mode: agentcard.ActorPolicyOwner, OpenID: "owner-1",
			},
			ResultPolicy: agentcard.ResultProjectionPolicy{
				ContinueAgent:       true,
				SuccessSurfaceState: agentcard.SurfaceStatusResolved,
				FailureSurfaceState: agentcard.SurfaceStatusFailed,
			},
		},
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotArgs != `{"task_id":"trusted"}` ||
		gotMeta.ChatID != "chat-1" ||
		gotMeta.OpenID != "owner-1" ||
		gotMeta.Data != nil ||
		gotCapabilityName != "schedule.update" ||
		gotIdempotencyKey != "cardcap_123" ||
		string(output) != `{"updated":true}` {
		t.Fatalf(
			"args=%q meta=%#v capability=%q output=%s",
			gotArgs,
			gotMeta,
			gotCapabilityName,
			output,
		)
	}
}

func TestAgentCardCapabilityExecutorRejectsUnregisteredOrPermissionMismatchedCalls(
	t *testing.T,
) {
	registry := toolkit.New[larkim.P2MessageReceiveV1]()
	registry.Add(
		toolkit.NewUnit[larkim.P2MessageReceiveV1]().
			Name("schedule.update").
			Func(func(
				context.Context,
				string,
				toolkit.FCMeta[larkim.P2MessageReceiveV1],
			) gresult.R[string] {
				return gresult.OK(`{}`)
			}),
	)
	executor, err := NewAgentCardCapabilityExecutor(registry)
	if err != nil {
		t.Fatal(err)
	}
	base := agentcard.CapabilityInvocation{
		Name: "schedule.update", Version: "v1", Input: json.RawMessage(`{}`),
		IdempotencyKey: "cardcap_123",
		Permission: agentcard.CapabilityPermissionContext{
			Scope: "schedule.update", ChatID: "chat-1", ActorOpenID: "owner-1",
		},
		ActorPolicy: agentcard.ActorPolicy{
			Mode: agentcard.ActorPolicyOwner, OpenID: "owner-1",
		},
		ResultPolicy: agentcard.ResultProjectionPolicy{
			ContinueAgent:       true,
			SuccessSurfaceState: agentcard.SurfaceStatusResolved,
			FailureSurfaceState: agentcard.SurfaceStatusFailed,
		},
	}
	tests := map[string]agentcard.CapabilityInvocation{
		"unregistered": func() agentcard.CapabilityInvocation {
			value := base
			value.Name = "permission.manage"
			value.Permission.Scope = "permission.manage"
			return value
		}(),
		"scope mismatch": func() agentcard.CapabilityInvocation {
			value := base
			value.Permission.Scope = "admin"
			return value
		}(),
		"actor mismatch": func() agentcard.CapabilityInvocation {
			value := base
			value.Permission.ActorOpenID = "forged"
			return value
		}(),
	}
	for name, invocation := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := executor.Execute(
				context.Background(),
				invocation,
			); err == nil {
				t.Fatal("Execute() error = nil")
			}
		})
	}
}

func TestAgentCardCapabilityExecutorPropagatesToolFailure(t *testing.T) {
	registry := toolkit.New[larkim.P2MessageReceiveV1]()
	wantErr := errors.New("tool rejected")
	registry.Add(
		toolkit.NewUnit[larkim.P2MessageReceiveV1]().
			Name("schedule.update").
			Func(func(
				context.Context,
				string,
				toolkit.FCMeta[larkim.P2MessageReceiveV1],
			) gresult.R[string] {
				return gresult.Err[string](wantErr)
			}),
	)
	executor, err := NewAgentCardCapabilityExecutor(registry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(
		context.Background(),
		agentcard.CapabilityInvocation{
			Name: "schedule.update", Version: "v1", Input: json.RawMessage(`{}`),
			IdempotencyKey: "cardcap_123",
			Permission: agentcard.CapabilityPermissionContext{
				Scope: "schedule.update", ChatID: "chat-1",
				ActorOpenID: "owner-1",
			},
			ActorPolicy: agentcard.ActorPolicy{
				Mode: agentcard.ActorPolicyOwner, OpenID: "owner-1",
			},
			ResultPolicy: agentcard.ResultProjectionPolicy{
				ContinueAgent:       true,
				SuccessSurfaceState: agentcard.SurfaceStatusResolved,
				FailureSurfaceState: agentcard.SurfaceStatusFailed,
			},
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v", err)
	}
}
