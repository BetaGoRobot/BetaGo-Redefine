package mcpstore

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUpdateDraftRequiresExpectedRevisionAndPendingUnexpiredRow(t *testing.T) {
	for _, affected := range []int64{1, 0} {
		t.Run(map[int64]string{1: "updated", 0: "conflict"}[affected], func(t *testing.T) {
			database, err := gorm.Open(postgres.New(postgres.Config{Conn: &orderFindDryRunPool{}}), &gorm.Config{DryRun: true, SkipDefaultTransaction: true})
			if err != nil {
				t.Fatal(err)
			}
			var updateSQL string
			var vars []any
			if err := database.Callback().Update().After("gorm:update").Register("test:draft-update", func(tx *gorm.DB) {
				updateSQL = tx.Statement.SQL.String()
				vars = append([]any(nil), tx.Statement.Vars...)
				tx.RowsAffected = affected
			}); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
			order := luckin.PendingOrder{ID: "pending-one", PayloadHash: "new-revision", CreateOrderPayload: json.RawMessage(`{"couponCodeList":["new-coupon"]}`), PreviewResult: json.RawMessage(`{"discountPrice":10}`)}
			err = NewPendingOrderRepository(database).UpdateDraft(context.Background(), order, "old-revision", now)
			if affected == 0 && !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
				t.Errorf("zero-row update error = %v, want conflict", err)
			}
			if affected != 0 && err != nil {
				t.Fatal(err)
			}
			update, where, ok := strings.Cut(updateSQL, " WHERE ")
			if !ok {
				t.Fatalf("update has no WHERE: %s", updateSQL)
			}
			if strings.Contains(where, " OR ") || strings.Count(where, " AND ") != 3 {
				t.Errorf("revision, ID, state and expiry must all match: %s", where)
			}
			assertDraftBoundValue(t, update, vars, "payload_hash", "=", order.PayloadHash)
			assertDraftBoundValue(t, where, vars, "id", "=", order.ID)
			assertDraftBoundValue(t, where, vars, "payload_hash", "=", "old-revision")
			assertDraftBoundValue(t, where, vars, "status", "=", string(luckin.PendingStatusPending))
			assertDraftBoundValue(t, where, vars, "expires_at", ">", now)
		})
	}
}

func TestUpdateDraftRejectsMissingExpectedRevisionBeforeDatabaseAccess(t *testing.T) {
	for _, hash := range []string{"", " \t\n"} {
		t.Run(strconv.Quote(hash), func(t *testing.T) {
			database, err := gorm.Open(postgres.New(postgres.Config{Conn: &orderFindDryRunPool{}}), &gorm.Config{DryRun: true, SkipDefaultTransaction: true})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			if err := database.Callback().Update().After("gorm:update").Register("test:unexpected-draft-update", func(tx *gorm.DB) { called = true; tx.RowsAffected = 1 }); err != nil {
				t.Fatal(err)
			}
			err = NewPendingOrderRepository(database).UpdateDraft(context.Background(), luckin.PendingOrder{ID: "pending-one", PayloadHash: "new-revision"}, hash, time.Now())
			if !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
				t.Errorf("missing revision error = %v, want not confirmable", err)
			}
			if called {
				t.Error("missing expected revision must not execute an update")
			}
		})
	}
}

func assertDraftBoundValue(t *testing.T, sql string, vars []any, column, operator string, want any) {
	t.Helper()
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(column) + `"\s*` + regexp.QuoteMeta(operator) + `\s*\$(\d+)`)
	match := pattern.FindStringSubmatch(sql)
	if len(match) != 2 {
		t.Errorf("missing %s %s predicate in %s", column, operator, sql)
		return
	}
	pos, err := strconv.Atoi(match[1])
	if err != nil || pos < 1 || pos > len(vars) {
		t.Fatalf("invalid bind position %q", match[1])
	}
	if got := vars[pos-1]; got != want {
		t.Errorf("%s binds %v, want %v", column, got, want)
	}
}
