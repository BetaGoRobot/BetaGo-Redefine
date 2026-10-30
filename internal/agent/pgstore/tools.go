package pgstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func (s *Store) approvalTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string) (domain.Approval, error) {
	var a domain.Approval
	var b []byte
	e := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("approvals")+` WHERE scope_key=$1 AND approval_id=$2`, scope.Key(), id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &a)
	}
	return a, e
}
func (s *Store) saveApprovalTx(ctx context.Context, tx *sql.Tx, a domain.Approval) error {
	b, e := encode(a)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE `+s.table("approvals")+` SET body=$3 WHERE scope_key=$1 AND approval_id=$2`, a.Scope.Key(), a.ID, b)
	return e
}
func (s *Store) Approvals(ctx context.Context, scope domain.Scope, taskID string) ([]domain.Approval, error) {
	if e := scope.Validate(); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("approvals")+` WHERE scope_key=$1 AND task_id=$2 ORDER BY revision,approval_id`, scope.Key(), taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.Approval
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var a domain.Approval
		if e = json.Unmarshal(b, &a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) FreezeApproval(ctx context.Context, j domain.Job, t domain.Task, name, args string, policy ...string) (domain.Approval, error) {
	var a domain.Approval
	canonical, e := domain.CanonicalArguments(args)
	if e != nil {
		return a, e
	}
	if name == "" || len(name) > 64 {
		return a, domain.ErrInvalid
	}
	e = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, e := s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		requested := ""
		if len(policy) > 0 {
			requested = policy[0]
		}
		digest, err := s.approvalPolicyTx(ctx, tx, name, requested)
		if err != nil {
			return err
		}
		id := "ap_" + domain.StableID(t.ID, fmt.Sprint(t.Revision), name, canonical)[:32]
		a, e = s.approvalTx(ctx, tx, t.Scope, id)
		if e == nil {
			if a.Status == "revoked" {
				return domain.ErrConflict
			}
			if a.PolicyDigest != digest {
				return domain.ErrUnauthorized
			}
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		a = domain.Approval{ID: id, TaskID: t.ID, Scope: t.Scope, Revision: t.Revision, Tool: name, Arguments: canonical, Status: "pending", CreatedAt: time.Now().UTC()}
		a.PolicyDigest = digest
		b, _ := encode(a)
		if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("approvals")+`(scope_key,task_id,approval_id,revision,body)VALUES($1,$2,$3,$4,$5)`, t.Scope.Key(), t.ID, a.ID, t.Revision, b); e != nil {
			return e
		}
		current.Status = "waiting"
		return s.saveTaskTx(ctx, tx, current)
	})
	return a, e
}
func (s *Store) BindInterrupt(ctx context.Context, j domain.Job, t domain.Task, approvalID, interruptID string) error {
	if interruptID == "" || len(interruptID) > 4096 {
		return domain.ErrInvalid
	}
	return s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		if _, e := s.guardTaskTx(ctx, tx, j, t); e != nil {
			return e
		}
		a, e := s.approvalTx(ctx, tx, t.Scope, approvalID)
		if e != nil {
			return e
		}
		if a.TaskID != t.ID || a.Revision != t.Revision || a.Status == "revoked" {
			return domain.ErrConflict
		}
		a.InterruptID = interruptID
		return s.saveApprovalTx(ctx, tx, a)
	})
}

type ToolPolicy struct {
	Name        string
	SideEffect  bool
	AutoExecute bool
	Digest      string
}

// SyncToolPolicies persists server-owned grants. Registry definitions have no
// model-controlled policy fields. Startup revokes removed definitions, but
// preserves existing revocations even after a definition is readded. Only the
// first insert grants a configured tool; restoring a grant is an operator action.
func (s *Store) SyncToolPolicies(ctx context.Context, policies []ToolPolicy) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "tool-policies:"+s.schema); e != nil {
			return e
		}
		names := make([]any, 0, len(policies))
		placeholders := make([]string, 0, len(policies))
		for _, p := range policies {
			if p.Name == "" || p.Digest == "" || (p.AutoExecute && !p.SideEffect) {
				return domain.ErrInvalid
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO `+s.table("tool_policies")+`(tool_name,side_effect,enabled,policy_digest,auto_execute)VALUES($1,$2,true,$3,$4)ON CONFLICT(tool_name)DO UPDATE SET side_effect=EXCLUDED.side_effect,policy_digest=EXCLUDED.policy_digest,auto_execute=EXCLUDED.auto_execute`, p.Name, p.SideEffect, p.Digest, p.AutoExecute); e != nil {
				return e
			}
			names = append(names, p.Name)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(names)))
		}
		removed := `UPDATE ` + s.table("tool_policies") + ` SET enabled=false`
		if len(names) > 0 {
			removed += ` WHERE tool_name NOT IN (` + strings.Join(placeholders, ",") + `)`
		}
		_, e := tx.ExecContext(ctx, removed, names...)
		return e
	})
}
func (s *Store) CheckToolPolicy(ctx context.Context, name, digest string, effect bool, automatic ...bool) error {
	var allowed bool
	q := `SELECT enabled AND policy_digest=$2 AND side_effect=$3`
	args := []any{name, digest, effect}
	if len(automatic) > 0 {
		q += ` AND auto_execute=$4`
		args = append(args, automatic[0])
	}
	e := s.db.QueryRowContext(ctx, q+` FROM `+s.table("tool_policies")+` WHERE tool_name=$1`, args...).Scan(&allowed)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !allowed {
		return domain.ErrUnauthorized
	}
	return e
}
func (s *Store) toolTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, taskID, key string) (domain.ToolExecution, error) {
	var x domain.ToolExecution
	var b []byte
	e := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("tool_executions")+` WHERE scope_key=$1 AND task_id=$2 AND execution_key=$3`, scope.Key(), taskID, key).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return x, domain.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &x)
	}
	return x, e
}
func (s *Store) ToolExecution(ctx context.Context, scope domain.Scope, taskID, key string) (domain.ToolExecution, error) {
	var x domain.ToolExecution
	e := s.transact(ctx, func(tx *sql.Tx) error { var e error; x, e = s.toolTx(ctx, tx, scope, taskID, key); return e })
	return x, e
}
func (s *Store) saveToolTx(ctx context.Context, tx *sql.Tx, x domain.ToolExecution) error {
	b, e := encode(x)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE `+s.table("tool_executions")+` SET status=$4,body=$5 WHERE scope_key=$1 AND task_id=$2 AND execution_key=$3`, x.Scope.Key(), x.TaskID, x.Key, x.Status, b)
	return e
}
func (s *Store) BeginTool(ctx context.Context, j domain.Job, t domain.Task, name, args, approvalID string, effect bool, policy ...string) (domain.ToolExecution, bool, error) {
	return s.beginTool(ctx, j, t, name, args, approvalID, effect, false, policy...)
}

// BeginToolForActorTurn retains the ordinary effect admission and uncertainty
// guards, but gives each authenticated actor/revision a separate receipt. The
// persisted task supplies identity; a caller's copied Task cannot override it.
func (s *Store) BeginToolForActorTurn(ctx context.Context, j domain.Job, t domain.Task, name, args, approvalID string, effect bool, policy ...string) (domain.ToolExecution, bool, error) {
	return s.beginTool(ctx, j, t, name, args, approvalID, effect, true, policy...)
}

func (s *Store) beginTool(ctx context.Context, j domain.Job, t domain.Task, name, args, approvalID string, effect, perActorTurn bool, policy ...string) (domain.ToolExecution, bool, error) {
	var x domain.ToolExecution
	if perActorTurn && !effect {
		return x, false, domain.ErrInvalid
	}
	reused := false
	canonical, e := domain.CanonicalArguments(args)
	if e != nil {
		return x, false, e
	}
	key := domain.StableID(t.ID, name, canonical)
	if !effect {
		// Reads are fresh observations with separate audit rows, including
		// repeated arguments within one turn or after a checkpoint resume.
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return x, false, err
		}
		key = domain.StableID(t.ID, name, canonical, "read", fmt.Sprintf("%x", nonce))
	}
	e = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, e := s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		if perActorTurn {
			if current.LastActorID == "" {
				return domain.ErrUnauthorized
			}
			key = domain.StableID(t.ID, name, canonical, "actor-turn", current.LastActorID, fmt.Sprint(current.Revision))
		}
		automatic := false
		if len(policy) > 0 {
			var allowed bool
			// The stored grant decides approval admission. The shared policy lock
			// also serializes revocation with the committed effect reservation.
			if e = tx.QueryRowContext(ctx, `SELECT enabled AND policy_digest=$2 AND side_effect=$3,auto_execute FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, name, policy[0], effect).Scan(&allowed, &automatic); errors.Is(e, sql.ErrNoRows) {
				return domain.ErrUnauthorized
			} else if e != nil {
				return e
			}
			if !allowed || (automatic && !effect) {
				return domain.ErrUnauthorized
			}
		}
		if automatic && approvalID != "" {
			return domain.ErrUnauthorized
		}
		x, e = s.toolTx(ctx, tx, t.Scope, t.ID, key)
		if e == nil {
			if effect && x.Status == "completed" {
				reused = true
				return nil
			}
			if effect {
				return domain.ErrEffectUnknown
			}
			return domain.ErrConflict
		} else if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if effect {
			var blocked bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("tool_executions")+` WHERE scope_key=$1 AND task_id=$2 AND status IN ('running_effect','unknown'))`, t.Scope.Key(), t.ID).Scan(&blocked); e != nil {
				return e
			}
			if blocked {
				return domain.ErrEffectUnknown
			}
		}
		if effect && !automatic {
			a, e := s.approvalTx(ctx, tx, t.Scope, approvalID)
			if e != nil {
				return e
			}
			if a.TaskID != t.ID || a.Revision != t.Revision || a.Tool != name || a.Arguments != canonical || (len(policy) > 0 && a.PolicyDigest != policy[0]) || a.Status != "approved" || !current.Authorized(a.ActorID) {
				return domain.ErrUnauthorized
			}
			if a.Source.MessageID != "" {
				if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{a.Source}); e != nil {
					return e
				}
				if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{a.Source}); e != nil {
					return e
				}
			}
			x.ActorID = a.ActorID
		} else {
			x.ActorID = current.LastActorID
		}
		actor := x.ActorID
		now := time.Now().UTC()
		x = domain.ToolExecution{Key: key, TaskID: t.ID, Scope: t.Scope, Revision: t.Revision, Tool: name, Arguments: canonical, ApprovalID: approvalID, ActorID: actor, Status: "running_read", JobID: j.ID, Fence: j.Fence, CreatedAt: now, UpdatedAt: now}
		if effect {
			x.Status = "running_effect"
		}
		b, _ := encode(x)
		_, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("tool_executions")+`(scope_key,task_id,execution_key,status,body)VALUES($1,$2,$3,$4,$5)ON CONFLICT(scope_key,task_id,execution_key)DO UPDATE SET status=EXCLUDED.status,body=EXCLUDED.body`, t.Scope.Key(), t.ID, key, x.Status, b)
		return e
	})
	return x, reused, e
}

// FinishTool records external receipts even after cancellation or lease loss.
// The caller receives the guard error and cannot publish a task continuation.
func (s *Store) FinishTool(ctx context.Context, j domain.Job, t domain.Task, input domain.ToolExecution, result string, cause error) error {
	if len(result) > 256<<10 {
		return domain.ErrInvalid
	}
	var validity error
	e := s.transact(ctx, func(tx *sql.Tx) error {
		_, validity = s.guardTaskTx(ctx, tx, j, t)
		x, e := s.toolTx(ctx, tx, t.Scope, t.ID, input.Key)
		if e != nil {
			return e
		}
		if x.JobID != j.ID || x.Fence != j.Fence {
			return domain.ErrStaleClaim
		}
		if x.Status == "completed" || x.Status == "completed_late" {
			return nil
		}
		x.UpdatedAt = time.Now().UTC()
		x.Result = result
		x.Status = "completed"
		if cause != nil {
			x.Error = "external_call_failed"
			if input.Status == "running_effect" {
				x.Status = "unknown"
			} else {
				x.Status = "failed_read"
			}
		} else if validity != nil && errors.Is(validity, domain.ErrConflict) {
			x.Status = "completed_late"
		}
		if e = s.saveToolTx(ctx, tx, x); e != nil {
			return e
		}
		if cause == nil && validity == nil && messageEffectTool(x.Tool) {
			var receipt struct {
				ID string `json:"message_id"`
			}
			if json.Unmarshal([]byte(result), &receipt) == nil && receipt.ID != "" {
				if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("task_sources")+`(scope_key,task_id,message_id)VALUES($1,$2,$3)ON CONFLICT DO NOTHING`, t.Scope.Key(), t.ID, receipt.ID); e != nil {
					return e
				}
			}
		}
		if cause == nil && validity == nil && x.ApprovalID != "" {
			a, e := s.approvalTx(ctx, tx, t.Scope, x.ApprovalID)
			if e != nil {
				return e
			}
			a.Status = "executed"
			return s.saveApprovalTx(ctx, tx, a)
		}
		return nil
	})
	if e != nil {
		return e
	}
	return validity
}

func messageEffectTool(name string) bool {
	switch name {
	case "feishu_send_message", "feishu_send_card", "feishu_send_image", "music_send_card", "music_send_audio":
		return true
	}
	return false
}

// Approval admission uses the same persisted server policy as execution. The
// shared row lock serializes grant revocation with the approval/outbox commit.
func (s *Store) approvalPolicyTx(ctx context.Context, tx *sql.Tx, name, digest string) (string, error) {
	var current string
	var allowed bool
	e := tx.QueryRowContext(ctx, `SELECT enabled AND side_effect AND NOT auto_execute,policy_digest FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, name).Scan(&allowed, &current)
	// An absent optional policy is retained only for storage-only callers that
	// have no executable registry. Native runtime approvals always carry a digest.
	if errors.Is(e, sql.ErrNoRows) && digest == "" {
		return "", nil
	}
	if errors.Is(e, sql.ErrNoRows) {
		return "", domain.ErrUnauthorized
	}
	if e != nil {
		return "", e
	}
	if !allowed || (digest != "" && digest != current) {
		return "", domain.ErrUnauthorized
	}
	return current, nil
}
