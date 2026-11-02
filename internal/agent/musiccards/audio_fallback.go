package musiccards

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musicmedia"
)

func (p *Service) audioFallbackKey(j domain.Job, f frame) string {
	return p.workKey(j, "audio-unsupported:"+strconv.Itoa(f.Sequence))
}

// A definite audio capability rejection terminates that immutable frame. Its
// replacement is another durable effect with a higher sequence, so lost fallback
// receipts replay only that exact replacement and never upload audio again.
func (p *Service) withoutAudio(ctx context.Context, j domain.Job, s session, original frame) error {
	if s.Request.Kind != "song" || !strings.Contains(original.Content, `"tag":"audio"`) {
		return business.ErrProvider
	}
	key := p.audioFallbackKey(j, original)
	var fallback frame
	stale := false
	err := p.jobTx(ctx, j, func(tx *sql.Tx) error {
		current, e := p.currentTx(ctx, tx, j, s.ID)
		if e != nil {
			return e
		}
		if current.Version != original.Version || current.State != "active" {
			stale = true
			return nil
		}
		fallback, e = p.readFrameQuery(ctx, tx, s.ID, key)
		if e == nil {
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if current.IntentVersion != s.IntentVersion {
			return domain.ErrConflict
		}
		if current.Sequence != original.Sequence {
			if _, e = tx.ExecContext(ctx, `UPDATE `+p.table("music_card_frames")+` SET state='superseded' WHERE session_id=$1 AND sequence=$2`, s.ID, original.Sequence); e != nil {
				return e
			}
			stale = true
			return nil
		}
		if _, e = tx.ExecContext(ctx, `UPDATE `+p.table("music_card_frames")+` SET state='unsupported' WHERE session_id=$1 AND sequence=$2`, s.ID, original.Sequence); e != nil {
			return e
		}
		current.Model.Audio = musicmedia.Audio{}
		current.Model.Notice = "当前卡片暂不支持内嵌音频，请使用播放器收听。"
		current.Sequence++
		content, e := p.render(current)
		if e != nil {
			return e
		}
		fallback = frame{Sequence: current.Sequence, Version: current.Version, UUID: domain.StableUUID(current.ID, strconv.Itoa(current.Sequence)), Content: content, Mode: "patch", State: "pending"}
		if e = p.insertFrame(ctx, tx, current, fallback, key); e != nil {
			return e
		}
		return p.save(ctx, tx, current)
	})
	if err != nil || stale {
		return err
	}
	if fallback.State == "sent" || fallback.State == "superseded" {
		return nil
	}
	return p.applyFrame(ctx, j, s, fallback)
}
