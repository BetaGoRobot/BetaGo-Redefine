package luckinaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
)

type pollRepositoryFake struct {
	updates          []mcpstore.OrderUpdate
	findCalls        int
	records          []luckin.OrderRecord
	appID, botOpenID string
}

func (r *pollRepositoryFake) ClaimDueOrders(_ context.Context, appID, botOpenID string, _ time.Time, _ time.Duration, _ int) ([]luckin.OrderRecord, error) {
	r.appID, r.botOpenID = appID, botOpenID
	return r.records, nil
}
func (r *pollRepositoryFake) FindRowID(context.Context, string, string, string) (int64, bool, error) {
	r.findCalls++
	return 7, true, nil
}
func (r *pollRepositoryFake) ApplyUpdate(_ context.Context, appID, botID string, _ int64, update mcpstore.OrderUpdate, _ time.Time) error {
	r.appID, r.botOpenID = appID, botID
	r.updates = append(r.updates, update)
	return nil
}

func TestOrderPollerClaimsOnlyCurrentBotOrders(t *testing.T) {
	repo := &pollRepositoryFake{}
	p := &OrderPoller{repo: repo, ctx: context.Background(), appID: "app", botOpenID: "bot"}
	p.tick()
	if repo.appID != "app" || repo.botOpenID != "bot" {
		t.Fatalf("wrong claiming tenant: %q/%q", repo.appID, repo.botOpenID)
	}
}

func TestOrderPollerRetriesFailedCardBeforeAcknowledgingTransition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		reminder bool
	}{
		{name: "making", status: luckin.OrderStatusMaking},
		{name: "placed", status: luckin.OrderStatusPlaced},
		{name: "completed", status: luckin.OrderStatusCompleted},
		{name: "cancelled", status: luckin.OrderStatusCancelled},
		{name: "unpaid_reminder", status: luckin.OrderStatusUnpaid, reminder: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cfg := luckin.DefaultOrderPollConfig()
			record := luckin.OrderRecord{AppID: "app", BotOpenID: "bot", OrderID: "order-two", MessageID: "child-two", Status: luckin.OrderRecordActive, LastRemoteStatus: luckin.OrderStatusUnpaid, CreatedAt: now.Add(-11 * time.Minute), PollDeadline: now.Add(time.Hour)}
			detail := luckin.OrderDetail{OrderID: record.OrderID, Status: tc.status}
			repo := &pollRepositoryFake{}
			fail := true
			calls := 0
			p := &OrderPoller{repo: repo, ctx: context.Background(), cfg: cfg, appID: "app", botOpenID: "bot", patch: func(_ context.Context, msgID string, _ any) error {
				calls++
				if msgID != "child-two" {
					t.Fatalf("patched sibling or parent: %s", msgID)
				}
				if fail {
					return errors.New("temporary card update failure")
				}
				return nil
			}}
			p.apply(record, 7, detail, luckin.EvaluatePoll(record, detail, cfg, now), now)
			if calls != 1 || len(repo.updates) != 1 {
				t.Fatalf("calls=%d updates=%d", calls, len(repo.updates))
			}
			failed := repo.updates[0]
			if failed.LastRemoteStatus != nil || failed.UnpaidReminded != nil || failed.Status != "" || failed.StoppedReason != "" || len(failed.Timestamps) != 0 {
				t.Fatalf("failed card was acknowledged, losing retry: %+v", failed)
			}
			if failed.NextPollAt == nil || !failed.NextPollAt.After(now) || failed.NextPollAt.After(now.Add(cfg.PollInterval)) {
				t.Fatalf("failed card must retry next interval, got %+v", failed.NextPollAt)
			}
			fail = false
			retryAt := *failed.NextPollAt
			p.apply(record, 7, detail, luckin.EvaluatePoll(record, detail, cfg, retryAt), retryAt)
			if calls != 2 || len(repo.updates) != 2 {
				t.Fatalf("missing retry: calls=%d updates=%d", calls, len(repo.updates))
			}
			success := repo.updates[1]
			if tc.reminder {
				if success.UnpaidReminded == nil || !*success.UnpaidReminded {
					t.Fatal("successful reminder not acknowledged")
				}
			} else if success.LastRemoteStatus == nil || *success.LastRemoteStatus != tc.status {
				t.Fatal("successful transition not acknowledged")
			}
			if luckin.IsTerminalOrderStatus(tc.status) && success.Status == "" {
				t.Fatal("terminal order must stop after successful patch")
			}
		})
	}
}

func TestOrderPollerReadyNoticeDeliveryAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		patchFail, sendFail, inferred, noMessage bool
	}{
		{name: "notice_failure", sendFail: true},
		{name: "patch_failure", patchFail: true},
		{name: "both_fail", patchFail: true, sendFail: true},
		{name: "status_name_only", inferred: true},
		{name: "missing_original_card", noMessage: true},
		{name: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cfg := luckin.DefaultOrderPollConfig()
			record := luckin.OrderRecord{AppID: "app", BotOpenID: "bot", OrderID: "ready-child", MessageID: "child-card", ChatID: "order-chat", RequesterOpenID: "buyer", InitiatorOpenID: "buyer", Status: luckin.OrderRecordActive, LastRemoteStatus: luckin.OrderStatusMaking, CreatedAt: now.Add(-time.Minute), PollDeadline: now.Add(time.Hour)}
			if tc.noMessage {
				record.MessageID = ""
			}
			detail := luckin.OrderDetail{OrderID: record.OrderID, Status: luckin.OrderStatusReady, StatusName: "等待取餐"}
			if tc.inferred {
				detail.Status = 0
			}
			repo := &pollRepositoryFake{}
			sends := 0
			failSend, failPatch := tc.sendFail, tc.patchFail
			p := &OrderPoller{repo: repo, ctx: context.Background(), cfg: cfg, appID: "app", botOpenID: "bot",
				patch: func(context.Context, string, any) error {
					if failPatch {
						return errors.New("patch failed")
					}
					return nil
				},
				create: func(_ context.Context, chatID string, _ any, msgID, suffix string) error {
					sends++
					if chatID != record.ChatID || msgID != "luckin-ready-"+record.OrderID || suffix != "_luckinReady" {
						t.Fatal("notice lost chat or stable per-order deduplication key")
					}
					if len(repo.updates) != 0 && sends == 1 {
						t.Error("ready transition acknowledged before notice delivery")
					}
					if failSend {
						return errors.New("send failed")
					}
					return nil
				},
			}
			p.apply(record, 7, detail, luckin.EvaluatePoll(record, detail, cfg, now), now)
			if sends != 1 {
				t.Fatalf("ready notice attempts=%d, want 1 even when original card cannot update", sends)
			}
			if len(repo.updates) != 1 {
				t.Fatalf("updates=%d", len(repo.updates))
			}
			update := repo.updates[0]
			if tc.sendFail || tc.patchFail {
				if update.LastRemoteStatus != nil || len(update.Timestamps) != 0 || update.Status != "" {
					t.Fatalf("failed delivery acknowledged: %+v", update)
				}
				if update.NextPollAt == nil || !update.NextPollAt.After(now) {
					t.Fatal("delivery failure must schedule retry")
				}
				failSend, failPatch = false, false
				retryAt := *update.NextPollAt
				p.apply(record, 7, detail, luckin.EvaluatePoll(record, detail, cfg, retryAt), retryAt)
				if sends != 2 {
					t.Fatal("missing ready notice retry")
				}
				update = repo.updates[1]
			}
			if update.LastRemoteStatus == nil || *update.LastRemoteStatus != luckin.OrderStatusReady {
				t.Fatalf("successful ready delivery not acknowledged: %+v", update)
			}
			record.LastRemoteStatus = *update.LastRemoteStatus
			before := sends
			p.apply(record, 7, detail, luckin.EvaluatePoll(record, detail, cfg, now.Add(time.Minute)), now.Add(time.Minute))
			if sends != before {
				t.Fatal("already acknowledged ready notice sent again")
			}
		})
	}
}

func TestOrderPollerRejectsForeignOrderBeforeAnySideEffects(t *testing.T) {
	for _, tc := range []struct{ name, app, bot, orderApp, orderBot string }{
		{"other_app", "app", "bot", "other", "bot"},
		{"other_bot", "app", "bot", "app", "other"},
		{"missing_order_app", "app", "bot", "", "bot"},
		{"missing_order_bot", "app", "bot", "app", ""},
		{"missing_worker_app", "", "bot", "app", "bot"},
		{"missing_worker_bot", "app", "", "app", "bot"},
		{"empty_identity", "", "", "", ""},
	} {
		for _, entry := range []string{"process", "apply"} {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				repo := &pollRepositoryFake{}
				tokens := &statusTokenStoreFake{token: "fake"}
				caller := &statusToolCallerFake{}
				sends := 0
				p := &OrderPoller{repo: repo, tokens: tokens, draft: luckin.NewDraftService(caller, "https://example.invalid"), ctx: context.Background(), cfg: luckin.DefaultOrderPollConfig(), appID: tc.app, botOpenID: tc.bot,
					patch:  func(context.Context, string, any) error { sends++; return nil },
					create: func(context.Context, string, any, string, string) error { sends++; return nil },
				}
				record := luckin.OrderRecord{AppID: tc.orderApp, BotOpenID: tc.orderBot, OrderID: "order-b", MessageID: "card", ChatID: "chat", CredentialScope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "buyer"}, LastRemoteStatus: luckin.OrderStatusMaking}
				now := time.Now()
				if entry == "process" {
					p.process(record, now)
				} else {
					detail := luckin.OrderDetail{OrderID: record.OrderID, Status: luckin.OrderStatusReady}
					p.apply(record, 7, detail, luckin.EvaluatePoll(record, detail, p.cfg, now), now)
				}
				if repo.findCalls != 0 || len(tokens.lookups) != 0 || len(caller.calls) != 0 || len(repo.updates) != 0 || sends != 0 {
					t.Fatalf("foreign order crossed boundary: row reads=%d credentials=%d remote=%d updates=%d sends=%d", repo.findCalls, len(tokens.lookups), len(caller.calls), len(repo.updates), sends)
				}
			})
		}
	}
}

func TestOrderPollerMixedBatchProcessesOnlyItsOwnBot(t *testing.T) {
	now := time.Now()
	records := []luckin.OrderRecord{
		{AppID: "app-a", BotOpenID: "bot-a", OrderID: "order-b", MessageID: "card-a", LastRemoteStatus: luckin.OrderStatusUnpaid, CreatedAt: now, CredentialScope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "buyer-a"}},
		{AppID: "app-b", BotOpenID: "bot-b", OrderID: "order-b", MessageID: "card-b", LastRemoteStatus: luckin.OrderStatusUnpaid, CreatedAt: now, CredentialScope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "buyer-b"}},
	}
	for _, own := range records {
		t.Run(own.BotOpenID, func(t *testing.T) {
			repo := &pollRepositoryFake{records: records}
			tokens := &statusTokenStoreFake{token: "fake"}
			caller := &statusToolCallerFake{}
			p := &OrderPoller{repo: repo, tokens: tokens, draft: luckin.NewDraftService(caller, "https://example.invalid"), ctx: context.Background(), cfg: luckin.DefaultOrderPollConfig(), appID: own.AppID, botOpenID: own.BotOpenID}
			p.tick()
			if repo.findCalls != 1 || len(tokens.lookups) != 1 || len(caller.calls) != 1 || len(repo.updates) != 1 {
				t.Fatalf("mixed batch not isolated: row reads=%d tokens=%d remote=%d updates=%d", repo.findCalls, len(tokens.lookups), len(caller.calls), len(repo.updates))
			}
			lookup := tokens.lookups[0]
			if lookup.AppID != own.AppID || lookup.BotOpenID != own.BotOpenID || lookup.Scope != own.CredentialScope || repo.appID != own.AppID || repo.botOpenID != own.BotOpenID {
				t.Fatal("credential read or state write used another bot")
			}
		})
	}
}
