package luckinaction

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type confirmRefreshService struct {
	err           error
	recovery      map[string]any
	requests      []luckin.ConfirmRequest
	recoveryCalls int
	recoveryID    string
	recoveryErr   error
}

func (s *confirmRefreshService) Confirm(_ context.Context, req luckin.ConfirmRequest) (map[string]any, error) {
	s.requests = append(s.requests, req)
	return map[string]any{"success": true}, s.err
}
func (*confirmRefreshService) Cancel(context.Context, luckin.CancelRequest) error { return nil }
func (s *confirmRefreshService) CardAfterConfirmError(_ context.Context, id string, err error, _ string) map[string]any {
	s.recoveryCalls++
	s.recoveryID = id
	s.recoveryErr = err
	return s.recovery
}

func TestConfirmAsyncRecoveryOnlyUpdatesOriginalChildCard(t *testing.T) {
	cause := errors.New("coupon already used")
	recovery := luckin.BuildOrderFailedCard("请刷新优惠券")
	service := &confirmRefreshService{err: cause, recovery: recovery}
	cards := map[string]any{"parent": "cart", "child-one": "order-one", "child-two": "order-two"}
	patches := 0
	locks := 0
	handler := handleConfirmWithEffects(service, nil, confirmEffects{
		patch: func(_ context.Context, msgID string, card any) error { patches++; cards[msgID] = card; return nil },
		lock: func(_ context.Context, msgID string, fn func() error) error {
			locks++
			if msgID != "child-two" {
				t.Fatalf("lock message = %s", msgID)
			}
			if patches != 0 {
				t.Error("confirmation patched before acquiring its lock")
			}
			return fn()
		},
	})
	action := testActionContextWithMsg(map[string]any{cardactionproto.PendingOrderIDField: "pending-two", cardactionproto.PayloadHashField: "hash-two"}, "child-two")
	task, err := handler(context.Background(), action)
	if err != nil || task == nil {
		t.Fatalf("task missing: %v", err)
	}
	if patches != 0 || locks != 0 || len(service.requests) != 0 {
		t.Fatal("synchronous handler performed async effects")
	}
	task(context.Background())
	if patches != 1 || locks != 1 {
		t.Errorf("patches=%d locks=%d, want one recovery patch and lock", patches, locks)
	}
	if cards["parent"] != "cart" || cards["child-one"] != "order-one" || !reflect.DeepEqual(cards["child-two"], recovery) {
		t.Fatal("recovery did not preserve sibling cards or use the returned recovery card")
	}
	if len(service.requests) != 1 || service.requests[0].MessageID != "child-two" || service.requests[0].PendingOrderID != "pending-two" || service.requests[0].PayloadHash != "hash-two" {
		t.Fatalf("unexpected confirmation requests: %+v", service.requests)
	}
	if service.recoveryCalls != 1 || service.recoveryID != "pending-two" || !errors.Is(service.recoveryErr, cause) {
		t.Fatal("recovery did not receive original order and cause")
	}
}

func TestConfirmAsyncStaleOrBusyCallbacksPreserveExistingCard(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause       error
		busy        bool
		nilRecovery bool
	}{
		{name: "already_done", cause: fmt.Errorf("confirm: %w", luckin.ErrPendingOrderAlreadyDone)},
		{name: "stale_hash", cause: fmt.Errorf("confirm: %w", luckin.ErrPendingOrderPayloadMismatch)},
		{name: "lock_busy", cause: fmt.Errorf("lock: %w", mcpstore.ErrSessionLocked), busy: true},
		{name: "nil_recovery", cause: errors.New("remote rejected"), nilRecovery: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &confirmRefreshService{err: tc.cause, recovery: map[string]any{"replace": true}}
			if tc.nilRecovery {
				service.recovery = nil
			}
			patches := 0
			handler := handleConfirmWithEffects(service, nil, confirmEffects{
				patch: func(context.Context, string, any) error { patches++; return nil },
				lock: func(_ context.Context, _ string, fn func() error) error {
					if tc.busy {
						return tc.cause
					}
					return fn()
				},
			})
			task, err := handler(context.Background(), testActionContextWithMsg(map[string]any{cardactionproto.PendingOrderIDField: "pending-two", cardactionproto.PayloadHashField: "hash-two"}, "child-two"))
			if err != nil || task == nil {
				t.Fatalf("task missing: %v", err)
			}
			task(context.Background())
			if patches != 0 {
				t.Errorf("callback replaced existing card %d times", patches)
			}
			wantRecovery := 0
			if tc.nilRecovery {
				wantRecovery = 1
			}
			if service.recoveryCalls != wantRecovery {
				t.Errorf("recovery calls=%d, want %d", service.recoveryCalls, wantRecovery)
			}
			wantConfirm := 1
			if tc.busy {
				wantConfirm = 0
			}
			if len(service.requests) != wantConfirm {
				t.Errorf("confirm calls=%d, want %d", len(service.requests), wantConfirm)
			}
		})
	}
}
