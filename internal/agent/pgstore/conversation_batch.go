package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// QueueConversationPlan freezes the complete terminal choice and all sends in
// one transaction. Sections append to the same card; no partly frozen plan
// can reach transport.
// A valid observation closes the turn and task without creating a send job.
func (s *Store) QueueConversationPlan(ctx context.Context, j domain.Job, t domain.Task, plan domain.ConversationPlan) error {
	frames, err := domain.ConversationFrames(plan)
	if err != nil {
		return err
	}
	return s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, err := s.guardTaskTx(ctx, tx, j, t)
		if err != nil {
			return err
		}
		if current.FinalConversation != nil {
			old, _ := json.Marshal(current.FinalConversation)
			next, _ := json.Marshal(plan)
			if string(old) != string(next) {
				return domain.ErrConflict
			}
			return nil
		}
		if current.Status == "ready_delivery" {
			return domain.ErrConflict
		}
		// Only server-pinned current/member scene sources are reply locators. IDs
		// proposed by the model never extend scope or turn material provenance.
		targets := map[string]bool{}
		for _, ref := range append(append([]domain.SourceRef{current.Root}, current.Sources...), current.ContextSources...) {
			if ref.Scope == current.Scope {
				targets[ref.MessageID] = true
			}
		}
		for _, id := range current.ReplyTargets {
			if err = s.validateSceneReplyTx(ctx, tx, current, id); err != nil {
				return err
			}
			targets[id] = true
		}
		for _, m := range plan.Messages {
			if !targets[m.ReplyToMessageID] {
				return domain.ErrUnauthorized
			}
		}
		batchID := "batch_" + domain.StableID(t.ID, fmt.Sprint(t.Revision))[:32]

		previous := ""
		initialID := "del_" + domain.StableID(t.ID, fmt.Sprint(t.Revision), "conversation", "0")[:32]
		for i, frame := range frames {
			id := "del_" + domain.StableID(t.ID, fmt.Sprint(t.Revision), "conversation", fmt.Sprint(i))[:32]
			d := domain.Delivery{ID: id, TaskID: t.ID, Scope: t.Scope, Revision: t.Revision, Kind: "final", MessageType: frame.MessageType, Content: frame.Content, ConversationText: frame.Text, UUID: domain.StableUUID(t.Scope.Key(), initialID), ParentID: plan.Messages[0].ReplyToMessageID, BatchID: batchID, Sequence: i, BatchSize: len(frames), PreviousID: previous}
			if i > 0 {
				d.UpdateOf = initialID
				d.UUID = domain.StableUUID(t.Scope.Key(), id, "update")
			}
			if _, err = s.queueDeliveryRecordTx(ctx, tx, current, d); err != nil {
				return err
			}
			previous = id
		}

		current.FinalConversation = &plan
		current.ActiveJobID = 0
		current.Status = "ready_delivery"
		turnStatus := "completed"
		if plan.Choice == "observe" {
			current.Status = "completed"
			turnStatus = "observed"
		}
		if err = s.saveTaskTx(ctx, tx, current); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE `+s.table("turns")+` SET status=$6,updated_at=clock_timestamp() WHERE scope_key=$1 AND task_id=$2 AND revision=$3 AND job_id=$4 AND fence=$5`, t.Scope.Key(), t.ID, t.Revision, j.ID, j.Fence, turnStatus)
		return err
	})
}

// Scene reply IDs are supplied only by the backend prompt builder. Every
// confirmed utterance keeps its own original source dependencies pinned.
func (s *Store) validateSceneReplyTx(ctx context.Context, tx *sql.Tx, t domain.Task, id string) error {
	var body []byte
	err := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("deliveries")+` WHERE scope_key=$1 AND message_id=$2 AND status='delivered' ORDER BY COALESCE((convert_from(body,'UTF8')::jsonb->>'Sequence')::int,0) DESC,`+rfc3339NanoEpochSQL(`(convert_from(body,'UTF8')::jsonb->>'UpdatedAt')`)+` DESC,delivery_id DESC LIMIT 1`, t.Scope.Key(), id).Scan(&body)
	if err == sql.ErrNoRows {
		return domain.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	var d domain.Delivery
	if json.Unmarshal(body, &d) != nil || d.Scope != t.Scope || d.MessageID != id || d.Kind != "final" || d.Status != "delivered" {
		return domain.ErrUnauthorized
	}
	if _, ok := domain.ConversationDeliveryText(d); !ok {
		return domain.ErrUnauthorized
	}
	refs, err := s.conversationDeliverySourcesTx(ctx, tx, d)
	if err != nil {
		return err
	}
	pinned := map[domain.SourceRef]bool{}
	for _, ref := range append(append([]domain.SourceRef(nil), t.Sources...), t.ContextSources...) {
		pinned[ref] = true
	}
	for _, ref := range refs {
		if !pinned[ref] {
			return domain.ErrUnauthorized
		}
	}
	return nil
}
