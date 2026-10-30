package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func (s *Store) deliveryTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, id string, lock ...bool) (domain.Delivery, error) {
	var d domain.Delivery
	var b []byte
	query := `SELECT body FROM ` + s.table("deliveries") + ` WHERE scope_key=$1 AND delivery_id=$2`
	if len(lock) > 0 && lock[0] {
		query += ` FOR UPDATE`
	}
	e := tx.QueryRowContext(ctx, query, scope.Key(), id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return d, domain.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &d)
	}
	return d, e
}
func (s *Store) Delivery(ctx context.Context, scope domain.Scope, id string) (domain.Delivery, error) {
	var d domain.Delivery
	e := s.transact(ctx, func(tx *sql.Tx) error { var e error; d, e = s.deliveryTx(ctx, tx, scope, id); return e })
	return d, e
}
func (s *Store) queueDeliveryTx(ctx context.Context, tx *sql.Tx, t domain.Task, kind, approvalID, msgType, content string) (domain.Delivery, error) {
	var d domain.Delivery
	if len(content) > 28<<10 || !json.Valid([]byte(content)) {
		return d, domain.ErrInvalid
	}
	id := "del_" + domain.StableID(t.ID, fmt.Sprint(t.Revision), kind, approvalID)[:32]
	d = domain.Delivery{ID: id, TaskID: t.ID, Scope: t.Scope, Revision: t.Revision, ApprovalID: approvalID, Kind: kind, MessageType: msgType, Content: content, UUID: domain.StableUUID(t.Scope.Key(), id), ParentID: t.Root.MessageID}
	return s.queueDeliveryRecordTx(ctx, tx, t, d)
}
func (s *Store) queueDeliveryRecordTx(ctx context.Context, tx *sql.Tx, t domain.Task, d domain.Delivery) (domain.Delivery, error) {
	if len(d.Content) > domain.MaxConversationBytes || !json.Valid([]byte(d.Content)) {
		return d, domain.ErrInvalid
	}
	existing, e := s.deliveryTx(ctx, tx, t.Scope, d.ID)
	if e == nil {
		if existing.Content != d.Content || existing.MessageType != d.MessageType || existing.ParentID != d.ParentID || existing.BatchID != d.BatchID || existing.Sequence != d.Sequence || existing.BatchSize != d.BatchSize || existing.PreviousID != d.PreviousID || existing.UpdateOf != d.UpdateOf || existing.ConversationText != d.ConversationText || existing.UUID != d.UUID || existing.Scope != d.Scope || existing.TaskID != d.TaskID || existing.Revision != d.Revision || existing.Kind != d.Kind || existing.ApprovalID != d.ApprovalID {
			return existing, domain.ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return d, e
	}
	if d.Kind == "final" {
		d.Sources = conversationSourceSnapshot(t)
	}
	now := time.Now().UTC()
	d.Status = "pending"
	d.CreatedAt = now
	d.UpdatedAt = now
	b, _ := encode(d)
	if _, e = tx.ExecContext(ctx, `INSERT INTO `+s.table("deliveries")+`(scope_key,delivery_id,task_id,revision,status,body)VALUES($1,$2,$3,$4,'pending',$5)`, t.Scope.Key(), d.ID, t.ID, t.Revision, b); e != nil {
		return d, e
	}
	payload, _ := json.Marshal(map[string]string{"delivery_id": d.ID})
	_, e = s.enqueueTx(ctx, tx, domain.JobSpec{Kind: domain.JobDelivery, Scope: t.Scope, Source: t.Root, Payload: payload, DedupeKey: d.ID, ExpiresAt: t.ExpiresAt})
	return d, e
}

func (s *Store) QueueApprovalDelivery(ctx context.Context, j domain.Job, t domain.Task, a domain.Approval, content string) error {
	return s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, e := s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		stored, e := s.approvalTx(ctx, tx, t.Scope, a.ID)
		if e != nil {
			return e
		}
		if stored.TaskID != t.ID || stored.Revision != t.Revision || stored.InterruptID == "" || stored.Status != "pending" {
			return domain.ErrConflict
		}
		_, e = s.queueDeliveryTx(ctx, tx, current, "approval", a.ID, "interactive", content)
		return e
	})
}
func (s *Store) QueueFinalDelivery(ctx context.Context, j domain.Job, t domain.Task, text string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	return s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, e := s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		if current.FinalConversation != nil {
			return domain.ErrConflict
		}
		if _, e = s.queueDeliveryTx(ctx, tx, current, "final", "", "text", string(content)); e != nil {
			return e
		}
		current.Status = "ready_delivery"
		return s.saveTaskTx(ctx, tx, current)
	})
}
func (s *Store) CheckDelivery(ctx context.Context, j domain.Job, id string) (domain.Delivery, error) {
	var d domain.Delivery
	e := s.transact(ctx, func(tx *sql.Tx) error {
		var e error
		d, e = s.deliveryTx(ctx, tx, j.Scope, id)
		if e != nil {
			return e
		}
		t, e := s.taskTx(ctx, tx, j.Scope, d.TaskID, false)
		if e != nil {
			return e
		}
		t.Revision = d.Revision
		t, e = s.guardTaskTx(ctx, tx, j, t)
		if e != nil {
			return e
		}
		// A previous sender can record its receipt while we wait for the job
		// lock. Reload under the delivery lock before deciding to send again.
		d, e = s.deliveryTx(ctx, tx, j.Scope, id, true)
		if e != nil {
			return e
		}
		if d.Status == "delivered" {
			return nil
		}
		if d.Status == "delivered_late" {
			if d.MessageID == "" || d.Kind == "final" && t.Status != "ready_delivery" {
				return domain.ErrConflict
			}
			// The provider already confirmed this exact frozen delivery. Only
			// a valid new claim for the unchanged task may reconcile it.
			d.Status = "delivered"
			d.UpdatedAt = time.Now().UTC()
			b, e := encode(d)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET status=$3,body=$4 WHERE scope_key=$1 AND delivery_id=$2`, d.Scope.Key(), d.ID, d.Status, b); e != nil {
				return e
			}
			if d.Kind == "final" {
				var pending bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND task_id=$2 AND revision=$3 AND status<>'delivered' AND convert_from(body,'UTF8')::jsonb->>'Kind'='final')`, t.Scope.Key(), t.ID, t.Revision).Scan(&pending); e != nil {
					return e
				}
				if pending {
					return s.checkRuntimeClaimTx(ctx, tx, j, t)
				}
				t.Status = "completed"
				if e = s.saveTaskTx(ctx, tx, t); e != nil {
					return e
				}
			}
			return s.checkRuntimeClaimTx(ctx, tx, j, t)
		}
		if d.Status != "pending" {
			return domain.ErrConflict
		}
		if d.PreviousID != "" {
			previous, err := s.deliveryTx(ctx, tx, d.Scope, d.PreviousID)
			if err != nil {
				return err
			}
			if previous.TaskID != d.TaskID || previous.Revision != d.Revision || previous.BatchID != d.BatchID || previous.Sequence+1 != d.Sequence {
				return domain.ErrConflict
			}
			if previous.Status != "delivered" || previous.MessageID == "" {
				return domain.ErrDeliveryPending
			}
		}
		if d.UpdateOf != "" {
			initial, err := s.cardUpdateTargetTx(ctx, tx, d)
			if err != nil {
				return err
			}
			d.MessageID = initial.MessageID
		}

		if d.Kind == "approval" {
			a, e := s.approvalTx(ctx, tx, j.Scope, d.ApprovalID)
			if e != nil {
				return e
			}
			if a.Status != "pending" {
				return domain.ErrConflict
			}
		} else if t.Status != "ready_delivery" {
			return domain.ErrConflict
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, t)
	})
	return d, e
}

// A send can finish after cancellation. Persist its provider receipt without
// completing or reviving the canceled/revised task.
func (s *Store) FinishDelivery(ctx context.Context, j domain.Job, input domain.Delivery, messageID string) error {
	if messageID == "" || len(messageID) > 1024 {
		return domain.ErrInvalid
	}
	var validity error
	e := s.transact(ctx, func(tx *sql.Tx) error {
		t, e := s.taskTx(ctx, tx, j.Scope, input.TaskID, false)
		if e != nil {
			return e
		}
		t.Revision = input.Revision
		_, v := s.guardTaskTx(ctx, tx, j, t)
		validity = v
		d, e := s.deliveryTx(ctx, tx, j.Scope, input.ID, true)
		if e != nil {
			return e
		}
		if d.Status == "delivered" || d.Status == "delivered_late" {
			return nil
		}
		if d.UpdateOf != "" {
			initial, err := s.cardUpdateTargetTx(ctx, tx, d)
			if err != nil {
				return err
			}
			if initial.MessageID != messageID {
				return domain.ErrConflict
			}
		}

		d.MessageID = messageID
		d.UpdatedAt = time.Now().UTC()
		d.ConfirmedAt = d.UpdatedAt
		// Commit the known receipt before attempting lifecycle advancement.
		// A lease, source or revision can change while the Send is in flight.
		d.Status = "delivered_late"
		b, _ := encode(d)
		if _, e = tx.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET message_id=$3,status=$4,body=$5 WHERE scope_key=$1 AND delivery_id=$2`, d.Scope.Key(), d.ID, messageID, d.Status, b); e != nil {
			return e
		}
		return nil
	})
	if e != nil {
		return e
	}
	if validity != nil {
		return validity
	}
	// Both successful senders and reclaimed workers reconcile through the same
	// guarded path. An older fence's receipt cannot strand a current sender.
	_, e = s.CheckDelivery(ctx, j, input.ID)
	return e
}

// A frame may only mutate its own confirmed initial ordinary card. Approval
// cards and other tasks/revisions can never become update targets.
func (s *Store) cardUpdateTargetTx(ctx context.Context, tx *sql.Tx, d domain.Delivery) (domain.Delivery, error) {
	initial, err := s.deliveryTx(ctx, tx, d.Scope, d.UpdateOf)
	if err != nil {
		return initial, err
	}
	if d.Kind != "final" || d.ApprovalID != "" || d.MessageType != "interactive" || d.UUID != domain.StableUUID(d.Scope.Key(), d.ID, "update") || d.Sequence < 1 || d.PreviousID == "" || initial.Scope != d.Scope || initial.TaskID != d.TaskID || initial.Revision != d.Revision || initial.Kind != "final" || initial.ApprovalID != "" || initial.MessageType != "interactive" || initial.Status != "delivered" || initial.MessageID == "" || initial.UpdateOf != "" || initial.Sequence != 0 || initial.BatchID == "" || initial.BatchID != d.BatchID || initial.BatchSize != d.BatchSize {
		return initial, domain.ErrConflict
	}

	// Feishu GET exposes a simplified card body without original config. The
	// backend-frozen initial payload proves the card was created shareable; the
	// next frozen frame must retain that flag as well.
	for _, content := range []string{initial.Content, d.Content} {
		var card struct {
			Config struct {
				UpdateMulti bool `json:"update_multi"`
			} `json:"config"`
		}
		if json.Unmarshal([]byte(content), &card) != nil || !card.Config.UpdateMulti {
			return initial, domain.ErrConflict
		}
	}
	return initial, nil
}
