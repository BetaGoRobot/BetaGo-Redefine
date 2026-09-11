package mcpstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type orderFindDryRunPool struct{}

func (*orderFindDryRunPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*orderFindDryRunPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("unexpected exec")
}
func (*orderFindDryRunPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (*orderFindDryRunPool) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected query row")
}

func TestFindOrderUsesTenantAndOrderPredicates(t *testing.T) {
	for _, found := range []bool{true, false} {
		t.Run(map[bool]string{true: "found", false: "missing"}[found], func(t *testing.T) {
			database, err := gorm.Open(postgres.New(postgres.Config{Conn: &orderFindDryRunPool{}}), &gorm.Config{DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			var querySQL string
			var vars []any
			err = database.Callback().Query().After("gorm:query").Register("test:order-row", func(tx *gorm.DB) {
				querySQL = tx.Statement.SQL.String()
				vars = append([]any(nil), tx.Statement.Vars...)
				if found {
					rows := tx.Statement.Dest.(*[]*model.LuckinOrder)
					*rows = []*model.LuckinOrder{{AppID: "app", BotOpenID: "bot", ChatID: "chat", OrderID: "order", RequesterOpenID: "buyer-b", CredentialScopeType: "personal", CredentialScopeID: "historical-a"}}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			record, err := NewOrderRepository(database).FindOrder(context.Background(), "app", "bot", "order")
			for _, predicate := range []string{`"app_id" = $1`, `"bot_open_id" = $2`, `"order_id" = $3`} {
				if !strings.Contains(querySQL, predicate) {
					t.Fatalf("missing %s in %s", predicate, querySQL)
				}
			}
			if len(vars) < 3 || !reflect.DeepEqual(vars[:3], []any{"app", "bot", "order"}) {
				t.Fatalf("unexpected query variables: %v", vars)
			}
			if !found {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("missing row error = %v", err)
				}
				return
			}
			if err != nil || record.OrderID != "order" || record.ChatID != "chat" || record.RequesterOpenID != "buyer-b" || record.CredentialScope != (luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "historical-a"}) {
				t.Fatalf("stored order metadata not preserved: %+v, error=%v", record, err)
			}
		})
	}
}

func TestClaimDueOrdersScopesSelectionAndLeaseToTenant(t *testing.T) {
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: &orderFindDryRunPool{}}), &gorm.Config{DryRun: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	var statements []struct {
		sql  string
		vars []any
	}
	capture := func(tx *gorm.DB) {
		statements = append(statements, struct {
			sql  string
			vars []any
		}{tx.Statement.SQL.String(), append([]any(nil), tx.Statement.Vars...)})
	}
	if err := database.Callback().Query().After("gorm:query").Register("test:claim-row", func(tx *gorm.DB) {
		capture(tx)
		rows := tx.Statement.Dest.(*[]*model.LuckinOrder)
		*rows = []*model.LuckinOrder{{ID: 7, AppID: "app", BotOpenID: "bot"}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Callback().Update().After("gorm:update").Register("test:claim-lease", capture); err != nil {
		t.Fatal(err)
	}
	_, err = NewOrderRepository(database).ClaimDueOrders(context.Background(), "app", "bot", time.Now(), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 {
		t.Fatalf("want selection and lease update, got %v", statements)
	}
	for _, statement := range statements {
		assertDraftBoundValue(t, statement.sql, statement.vars, "app_id", "=", "app")
		assertDraftBoundValue(t, statement.sql, statement.vars, "bot_open_id", "=", "bot")
	}

}

func TestClaimDueOrdersRejectsMissingTenantBeforeDatabaseAccess(t *testing.T) {
	for _, tenant := range [][2]string{{"", "bot"}, {"app", " "}, {"", ""}} {
		repo := &OrderRepository{}
		if _, err := repo.ClaimDueOrders(context.Background(), tenant[0], tenant[1], time.Now(), time.Minute, 10); err == nil {
			t.Errorf("missing tenant %q must not claim orders", tenant)
		}
	}
}

func TestApplyOrderUpdateRequiresTenantAndRow(t *testing.T) {
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: &orderFindDryRunPool{}}), &gorm.Config{DryRun: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	var statement string
	var vars []any
	if err := database.Callback().Update().After("gorm:update").Register("test:tenant-update", func(tx *gorm.DB) {
		statement = tx.Statement.SQL.String()
		vars = append([]any(nil), tx.Statement.Vars...)
		tx.RowsAffected = 1
	}); err != nil {
		t.Fatal(err)
	}
	if err := NewOrderRepository(database).ApplyUpdate(context.Background(), "app", "bot", 7, OrderUpdate{Status: luckin.OrderRecordCompleted}, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertDraftBoundValue(t, statement, vars, "app_id", "=", "app")
	assertDraftBoundValue(t, statement, vars, "bot_open_id", "=", "bot")
	assertDraftBoundValue(t, statement, vars, "id", "=", int64(7))
}

func TestApplyOrderUpdateRejectsMissingTenantBeforeDatabaseAccess(t *testing.T) {
	for _, tenant := range [][2]string{{"", "bot"}, {"app", " "}, {"", ""}} {
		repo := &OrderRepository{}
		if err := repo.ApplyUpdate(context.Background(), tenant[0], tenant[1], 7, OrderUpdate{Status: luckin.OrderRecordFailed}, time.Now()); err == nil {
			t.Fatalf("missing tenant accepted: %q", tenant)
		}
	}
}

func TestApplyOrderUpdateRejectsRowOutsideTenant(t *testing.T) {
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: &orderFindDryRunPool{}}), &gorm.Config{DryRun: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	// A row ID outside the tenant matches no rows in the scoped UPDATE.
	err = NewOrderRepository(database).ApplyUpdate(context.Background(), "app", "bot", 7, OrderUpdate{Status: luckin.OrderRecordCompleted}, time.Now())
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("zero matching rows error=%v", err)
	}
}
