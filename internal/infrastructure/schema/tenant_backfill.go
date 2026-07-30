package schema

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
)

func tenantBackfill(
	ctx context.Context,
	transaction *sql.Tx,
	schema string,
) error {
	if err := backfillRoots(
		ctx,
		transaction,
		schema,
		"agent_sessions",
	); err != nil {
		return err
	}
	if err := backfillRoots(
		ctx,
		transaction,
		schema,
		"evaluation_cohorts",
	); err != nil {
		return err
	}

	propagations := []string{
		`update %[1]s.agent_runs child
		 set tenant_id = parent.tenant_id
		 from %[1]s.agent_sessions parent
		 where child.session_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.agent_steps child
		 set tenant_id = parent.tenant_id
		 from %[1]s.agent_runs parent
		 where child.run_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.agent_capability_executions child
		 set tenant_id = parent.tenant_id
		 from %[1]s.agent_runs parent
		 where child.run_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.agent_projection_outbox child
		 set tenant_id = parent.tenant_id
		 from %[1]s.agent_steps parent
		 where child.step_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.agent_card_surfaces child
		 set tenant_id = parent.tenant_id
		 from %[1]s.agent_runs parent
		 where child.run_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.evaluation_episodes child
		 set tenant_id = parent.tenant_id
		 from %[1]s.evaluation_cohorts parent
		 where child.cohort_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.evaluation_episode_messages child
		 set tenant_id = parent.tenant_id
		 from %[1]s.evaluation_episodes parent
		 where child.episode_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.evaluation_candidate_tasks child
		 set tenant_id = parent.tenant_id
		 from %[1]s.evaluation_episodes parent
		 where child.episode_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.evaluation_lane_outputs child
		 set tenant_id = parent.tenant_id
		 from %[1]s.evaluation_episodes parent
		 where child.episode_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.evaluation_feedback child
		 set tenant_id = parent.tenant_id
		 from %[1]s.evaluation_episodes parent
		 where child.episode_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
		`update %[1]s.evaluation_judgments child
		 set tenant_id = parent.tenant_id
		 from %[1]s.evaluation_episodes parent
		 where child.episode_id = parent.id
		   and child.tenant_id is distinct from parent.tenant_id`,
	}
	quotedSchema := quoteIdentifier(schema)
	for index, template := range propagations {
		if _, err := transaction.ExecContext(
			ctx,
			fmt.Sprintf(template, quotedSchema),
		); err != nil {
			return fmt.Errorf("propagate tenant at step %d: %w", index+1, err)
		}
	}

	tables := []string{
		"agent_sessions",
		"agent_runs",
		"agent_steps",
		"agent_capability_executions",
		"agent_projection_outbox",
		"agent_card_surfaces",
		"evaluation_cohorts",
		"evaluation_episodes",
		"evaluation_episode_messages",
		"evaluation_candidate_tasks",
		"evaluation_lane_outputs",
		"evaluation_feedback",
		"evaluation_judgments",
	}
	for _, table := range tables {
		var unresolved int64
		statement := fmt.Sprintf(
			`select count(*) from %s.%s where tenant_id is null or tenant_id = ''`,
			quotedSchema,
			quoteIdentifier(table),
		)
		if err := transaction.QueryRowContext(ctx, statement).Scan(&unresolved); err != nil {
			return fmt.Errorf("count unresolved tenant rows in %s: %w", table, err)
		}
		if unresolved > 0 {
			return fmt.Errorf(
				"table %s has %d rows without a resolvable tenant",
				table,
				unresolved,
			)
		}
	}
	return nil
}

func backfillRoots(
	ctx context.Context,
	transaction *sql.Tx,
	schema string,
	table string,
) error {
	statement := fmt.Sprintf(
		`select id, app_id, bot_open_id, tenant_id from %s.%s`,
		quoteIdentifier(schema),
		quoteIdentifier(table),
	)
	rows, err := transaction.QueryContext(ctx, statement)
	if err != nil {
		return fmt.Errorf("load tenant roots from %s: %w", table, err)
	}
	defer rows.Close()

	type update struct {
		id       string
		tenantID string
	}
	updates := make([]update, 0)
	for rows.Next() {
		var id, appID, botOpenID string
		var stored sql.NullString
		if err = rows.Scan(&id, &appID, &botOpenID, &stored); err != nil {
			return fmt.Errorf("scan tenant root from %s: %w", table, err)
		}
		identity, identityErr := tenant.New(appID, botOpenID)
		if identityErr != nil {
			return fmt.Errorf("resolve tenant root %s in %s: %w", id, table, identityErr)
		}
		if stored.Valid && stored.String != "" && stored.String != identity.ID {
			return fmt.Errorf(
				"tenant root %s in %s has conflicting tenant_id",
				id,
				table,
			)
		}
		if !stored.Valid || stored.String == "" {
			updates = append(updates, update{id: id, tenantID: identity.ID})
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate tenant roots from %s: %w", table, err)
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close tenant roots from %s: %w", table, err)
	}

	updateStatement := fmt.Sprintf(
		`update %s.%s set tenant_id = $1 where id = $2`,
		quoteIdentifier(schema),
		quoteIdentifier(table),
	)
	for _, item := range updates {
		if _, err = transaction.ExecContext(
			ctx,
			updateStatement,
			item.tenantID,
			item.id,
		); err != nil {
			return fmt.Errorf("update tenant root %s in %s: %w", item.id, table, err)
		}
	}
	return nil
}
