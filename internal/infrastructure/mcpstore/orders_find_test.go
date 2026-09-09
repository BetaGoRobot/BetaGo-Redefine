package mcpstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

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
