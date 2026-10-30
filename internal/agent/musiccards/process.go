package musiccards

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musicmedia"
)

// HandleAction accepts only SDK-derived identity. Its signed payload grants no
// identity and cannot expand the scope, displayed item set, or pinned message.
func (p *Service) HandleAction(ctx context.Context, in ActionInvocation) error {
	if in.Scope.Validate() != nil || in.ActorID == "" || in.EventID == "" || len(in.EventID) > 1024 || in.MessageID == "" {
		return domain.ErrUnauthorized
	}
	c, e := verifyAction(p.key, in.Token, time.Now())
	if e != nil || c.Scope != in.Scope {
		return domain.ErrUnauthorized
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	digest := domain.StableID(in.Scope.Key(), in.ActorID, in.MessageID, in.Token)
	return p.tx(bounded, func(tx *sql.Tx) error {
		// Scope-wide event identity is checked before current view version, so an
		// identical delivery remains successful after its first navigation.
		_, e := tx.ExecContext(bounded, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.Scope.Key()+":music-event:"+in.EventID)
		if e != nil {
			return e
		}
		var existing string
		e = tx.QueryRowContext(bounded, `SELECT digest FROM `+p.table("music_card_events")+` WHERE scope_key=$1 AND event_id=$2`, in.Scope.Key(), in.EventID).Scan(&existing)
		if e == nil {
			if existing != digest {
				return domain.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		s, e := p.load(bounded, tx, c.Session, true)
		if e != nil {
			return domain.ErrUnauthorized
		}
		if s.Identity.Scope != in.Scope || s.MessageID != in.MessageID || s.State != "active" || time.Now().After(s.Expires) {
			return domain.ErrUnauthorized
		}
		if c.Version != s.Version {
			return domain.ErrConflict
		}
		work := c.Action
		request := s.Request
		job := continuation{Work: work, Event: in.EventID, Actor: in.ActorID, ID: c.ID}
		switch c.Action {
		case "page":
			if s.Request.Kind == "song" || c.Page < 1 || c.Page > min(2001, max(1, (s.Model.Total+4)/5)) || c.Page == s.Request.Page {
				return domain.ErrInvalid
			}
			request.Page = c.Page
			job.Work = "navigate"
		case "play", "album":
			selectedAlbum := c.Action == "album" && s.Request.Kind == "song" && s.Model.Song != nil && s.Model.Song.Item.AlbumID > 0 && s.Model.Song.Item.AlbumID == c.ID
			found := selectedAlbum
			for _, item := range s.Model.Items {
				if item.ID == c.ID {
					found = true
					break
				}
			}
			if !found {
				return domain.ErrUnauthorized
			}
			if c.Action == "play" {
				kind := s.Request.Scene
				if s.Request.Kind != "" {
					kind = "song"
				}
				if kind != "song" {
					return domain.ErrUnauthorized
				}
				request = Request{Kind: "song", ID: c.ID, Page: 1}
				job.Work = "navigate"
			} else if !selectedAlbum && s.Request.Scene != "album" && s.Request.Scene != "playlist" {
				return domain.ErrUnauthorized
			}
		case "lyrics":
			if s.Request.Kind != "song" || s.Request.ID != c.ID || s.Model.Song == nil {
				return domain.ErrUnauthorized
			}
		case "refresh":
			job.Work = "navigate"
			job.Refresh = true
		case "withdraw":
			if in.ActorID != s.Identity.ActorID && (p.admin == nil || !p.admin(in.Scope, in.ActorID)) {
				return domain.ErrUnauthorized
			}
		default:
			return domain.ErrUnauthorized
		}
		if job.Work == "navigate" || job.Work == "withdraw" {
			s.IntentVersion++
			s.Pending = &navigationIntent{Event: job.Event, Actor: job.Actor, Work: job.Work, BaseVersion: s.Version, Request: request, Refresh: job.Refresh}
		}
		if _, e = tx.ExecContext(bounded, `INSERT INTO `+p.table("music_card_events")+`(scope_key,event_id,digest,session_id) VALUES($1,$2,$3,$4)`, in.Scope.Key(), in.EventID, digest, s.ID); e != nil {
			return e
		}
		if e = p.save(bounded, tx, s); e != nil {
			return e
		}
		return p.enqueue(bounded, tx, s, job)
	})
}

func (p *Service) Process(ctx context.Context, j domain.Job) error {
	var c continuation
	if j.Kind != domain.JobPresentation || json.Unmarshal(j.Payload, &c) != nil || c.Type != "typed_music" || c.Session == "" || c.Version < 1 {
		return domain.ErrInvalid
	}
	if e := p.store.CheckClaim(ctx, j); e != nil {
		return e
	}
	s, e := p.load(ctx, p.db, c.Session, false)
	if e != nil {
		return e
	}
	if s.Identity.Scope != j.Scope || s.Source != j.Source {
		return domain.ErrUnauthorized
	}
	if s.State != "active" {
		return nil
	}
	navigation := c.Event != "" && (c.Work == "navigate" || c.Work == "withdraw")
	if navigation {
		s, c, e = p.admitNavigation(ctx, j, s, c)
		if e != nil {
			return e
		}
	} else if s.Version != c.Version {
		return nil
	}
	if s.Pending != nil && s.Pending.Event != c.Event {
		return domain.ErrConflict
	}
	if e = p.admit(ctx, s); e != nil {
		return e
	}
	if !navigation && (c.Event != "" || c.Actor != "") {
		if c.Event == "" || c.Actor == "" || p.member == nil {
			return domain.ErrUnauthorized
		}
		allowed, err := p.member(ctx, s.Identity.Scope, c.Actor)
		if err != nil {
			return business.ErrProvider
		}
		if !allowed {
			return domain.ErrUnauthorized
		}
	}
	// A pending frozen frame is retried before any provider reads. Its immutable
	// UUID/content/sequence survives a lost patch receipt and process restart.
	if done, e := p.resume(ctx, j, s, c); done || e != nil {
		return e
	}
	switch c.Work {
	case "navigate":
		if e = p.metadata(ctx, &s, c.Refresh); e != nil {
			return safeResourceError(e)
		}
		e = p.patch(ctx, j, s, c, "view", func(current *session) { current.Model = s.Model })
		if e != nil {
			return e
		}
		return p.jobTx(ctx, j, func(tx *sql.Tx) error {
			if e := p.claimTx(ctx, tx, j); e != nil {
				return e
			}
			current, e := p.currentTx(ctx, tx, j, s.ID)
			if e != nil {
				return e
			}
			if current.Version != c.Version {
				return nil
			}
			work := "enrich"
			if current.Request.Kind == "song" {
				work = "audio"
			}
			return p.enqueue(ctx, tx, current, continuation{Work: work, Refresh: c.Refresh, Actor: c.Actor, Event: c.Event})
		})
	case "audio":
		return p.duringView(ctx, j, s, func(workCtx context.Context) error {
			var audio musicmedia.Audio
			var err error
			if c.Refresh {
				if refresher, ok := p.media.(interface {
					RefreshAudio(context.Context, domain.Scope, int64) (musicmedia.Audio, error)
				}); ok {
					audio, err = refresher.RefreshAudio(workCtx, s.Identity.Scope, s.Request.ID)
				} else {
					audio, err = p.media.UploadAudio(workCtx, s.Identity.Scope, s.Request.ID, true)
				}
			} else {
				audio, err = p.media.UploadAudio(workCtx, s.Identity.Scope, s.Request.ID, false)
			}
			if workCtx.Err() != nil {
				return workCtx.Err()
			}
			if err != nil || audio.FileKey == "" {
				return p.patch(workCtx, j, s, c, "audio", func(current *session) {
					current.Model.Notice = "音频暂时不可用，可通过播放器收听或稍后刷新。"
				})
			}
			return p.patch(workCtx, j, s, c, "audio", func(current *session) { current.Model.Audio = audio; current.Model.Notice = "" })
		})
	case "enrich":
		return p.duringView(ctx, j, s, func(workCtx context.Context) error { return p.enrich(workCtx, j, s, c) })
	case "withdraw":
		if c.Actor != s.Identity.ActorID && (p.admin == nil || !p.admin(s.Identity.Scope, c.Actor)) {
			return domain.ErrUnauthorized
		}
		if e = p.store.CheckClaim(ctx, j); e != nil {
			return e
		}
		if e = p.admit(ctx, s); e != nil {
			return e
		}
		if e = p.messenger.Retract(ctx, s.Identity.Scope, s.MessageID); e != nil {
			return safeResourceError(e)
		}
		return p.jobTx(ctx, j, func(tx *sql.Tx) error {
			if e := p.claimTx(ctx, tx, j); e != nil {
				return e
			}
			current, e := p.currentTx(ctx, tx, j, s.ID)
			if e != nil {
				return e
			}
			if current.Version != c.Version {
				return nil
			}
			current.State = "withdrawn"
			return p.save(ctx, tx, current)
		})
	case "album", "lyrics":
		return p.reply(ctx, j, s, c)
	default:
		return domain.ErrInvalid
	}
}
func (p *Service) workKey(j domain.Job, suffix string) string {
	return strconv.FormatInt(j.ID, 10) + ":" + suffix
}
func (p *Service) readFrame(ctx context.Context, sessionID, key string) (frame, error) {
	return p.readFrameQuery(ctx, p.db, sessionID, key)
}
func (p *Service) readFrameQuery(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sessionID, key string) (frame, error) {
	var f frame
	e := q.QueryRowContext(ctx, `SELECT sequence,view_version,uuid,content,mode,state,message_id FROM `+p.table("music_card_frames")+` WHERE session_id=$1 AND work_key=$2`, sessionID, key).Scan(&f.Sequence, &f.Version, &f.UUID, &f.Content, &f.Mode, &f.State, &f.MessageID)
	if errors.Is(e, sql.ErrNoRows) {
		return f, domain.ErrNotFound
	}
	return f, e
}
func (p *Service) resume(ctx context.Context, j domain.Job, s session, c continuation) (bool, error) {
	if c.Work == "enrich" || c.Work == "withdraw" || c.Work == "album" || c.Work == "lyrics" {
		return false, nil
	}
	suffix := "view"
	if c.Work == "audio" {
		suffix = "audio"
	}
	f, e := p.readFrame(ctx, s.ID, p.workKey(j, suffix))
	if errors.Is(e, domain.ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if f.State != "sent" {
		if e = p.applyFrame(ctx, j, s, f); e != nil {
			return true, e
		}
	}
	if c.Work == "navigate" {
		e = p.jobTx(ctx, j, func(tx *sql.Tx) error {
			if e := p.claimTx(ctx, tx, j); e != nil {
				return e
			}
			current, e := p.currentTx(ctx, tx, j, s.ID)
			if e != nil {
				return e
			}
			if current.Version != c.Version {
				return nil
			}
			work := "enrich"
			if current.Request.Kind == "song" {
				work = "audio"
			}
			return p.enqueue(ctx, tx, current, continuation{Work: work, Refresh: c.Refresh, Actor: c.Actor, Event: c.Event})
		})
	}
	return true, e
}
func (p *Service) patch(ctx context.Context, j domain.Job, s session, c continuation, suffix string, merge func(*session)) error {
	key := p.workKey(j, suffix)
	f, e := p.readFrame(ctx, s.ID, key)
	if e == nil {
		if f.State == "sent" || f.State == "superseded" {
			return nil
		}
		return p.applyFrame(ctx, j, s, f)
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	stale := false
	e = p.jobTx(ctx, j, func(tx *sql.Tx) error {
		if e := p.claimTx(ctx, tx, j); e != nil {
			return e
		}
		current, e := p.currentTx(ctx, tx, j, s.ID)
		if e != nil {
			return e
		}
		if current.Version != c.Version || current.State != "active" {
			stale = true
			return nil
		}
		if current.IntentVersion != s.IntentVersion {
			return domain.ErrConflict
		}
		merge(&current)
		current.Sequence++
		content, e := p.render(current)
		if e != nil {
			return e
		}
		f = frame{Sequence: current.Sequence, Version: current.Version, UUID: domain.StableUUID(current.ID, strconv.Itoa(current.Sequence)), Content: content, Mode: "patch", State: "pending"}
		if e = p.insertFrame(ctx, tx, current, f, key); e != nil {
			return e
		}
		if e = p.save(ctx, tx, current); e != nil {
			return e
		}
		s = current
		return nil
	})
	if e != nil || stale {
		return e
	}
	return p.applyFrame(ctx, j, s, f)
}
func (p *Service) applyFrame(ctx context.Context, j domain.Job, s session, f frame) error {
	if f.State == "unsupported" {
		fallback, e := p.readFrame(ctx, s.ID, p.audioFallbackKey(j, f))
		if e != nil {
			return e
		}
		if fallback.State == "sent" || fallback.State == "superseded" {
			return nil
		}
		return p.applyFrame(ctx, j, s, fallback)
	}
	if e := p.store.CheckClaim(ctx, j); e != nil {
		return e
	}
	current, e := p.load(ctx, p.db, s.ID, false)
	if e != nil {
		return e
	}
	if current.Version != f.Version || current.State != "active" {
		return nil
	}
	if current.IntentVersion != s.IntentVersion {
		return domain.ErrConflict
	}
	if e = p.admit(ctx, current); e != nil {
		return e
	}
	if current.Sequence > f.Sequence {
		return p.jobTx(ctx, j, func(tx *sql.Tx) error {
			if e := p.claimTx(ctx, tx, j); e != nil {
				return e
			}
			_, e := tx.ExecContext(ctx, `UPDATE `+p.table("music_card_frames")+` SET state='superseded' WHERE session_id=$1 AND sequence=$2 AND state='pending'`, s.ID, f.Sequence)
			return e
		})
	}
	// CardKit's sequence fence also protects a request already in flight when a
	// newer navigation is admitted; every retry reuses this exact frozen frame.
	if e = p.messenger.UpdateMusicCard(ctx, current.Identity.Scope, current.MessageID, f.Content, f.UUID, f.Sequence); e != nil {
		if errors.Is(e, domain.ErrMusicAudioUnsupported) {
			return p.withoutAudio(ctx, j, current, f)
		}
		return business.ErrProvider
	}
	return p.jobTx(ctx, j, func(tx *sql.Tx) error {
		if e := p.claimTx(ctx, tx, j); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE `+p.table("music_card_frames")+` SET state='sent',message_id=$3 WHERE session_id=$1 AND sequence=$2`, s.ID, f.Sequence, current.MessageID)
		return e
	})
}

type enrichment struct {
	key     string
	id      int64
	image   string
	comment business.MusicComment
	isCover bool
	err     error
}

func (p *Service) enrich(ctx context.Context, j domain.Job, s session, c continuation) error {
	// Recover receipt uncertainty before allowing fresh resources to allocate a
	// later sequence. This also avoids refetching provider data after a restart.
	rows, e := p.db.QueryContext(ctx, `SELECT sequence,view_version,uuid,content,mode,state,message_id FROM `+p.table("music_card_frames")+` WHERE session_id=$1 AND work_key LIKE $2 AND state='pending' ORDER BY sequence`, s.ID, p.workKey(j, "")+"%")
	if e != nil {
		return e
	}
	var pending []frame
	for rows.Next() {
		var f frame
		if e = rows.Scan(&f.Sequence, &f.Version, &f.UUID, &f.Content, &f.Mode, &f.State, &f.MessageID); e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range pending {
		if e = p.applyFrame(ctx, j, s, f); e != nil {
			return e
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tasks := make(chan enrichment, 10)
	results := make(chan enrichment, 10)
	kind := s.Request.Scene
	if s.Request.Kind != "" {
		kind = "song"
	}
	for _, item := range s.Model.Items {
		for _, cover := range []bool{true, false} {
			suffix := "comment:"
			if cover {
				suffix = "cover:"
			}
			tasks <- enrichment{key: suffix + strconv.FormatInt(item.ID, 10), id: item.ID, image: item.ImageURL, isCover: cover}
		}
	}
	close(tasks)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				if ctx.Err() != nil {
					return
				}
				if _, e := p.readFrame(ctx, s.ID, p.workKey(j, task.key)); e == nil {
					results <- task
					continue
				}
				if task.isCover {
					resolve := p.media.ResolveCover
					if c.Refresh {
						if refresher, ok := p.media.(interface {
							RefreshCover(context.Context, domain.Scope, string, int64, string) (musicmedia.Cover, error)
						}); ok {
							resolve = refresher.RefreshCover
						}
					}
					cover, e := resolve(ctx, s.Identity.Scope, kind, task.id, task.image)
					task.image = cover.ImageKey
					task.err = e
				} else {
					task.comment, task.err = p.provider.Comment(ctx, kind, task.id)
				}
				results <- task
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()
	var first error
	for result := range results {
		if first != nil {
			continue
		}
		if result.err != nil {
			if result.isCover {
				result.image = ""
			} else {
				result.comment = business.MusicComment{}
			}
		}
		result := result
		if e := p.patch(ctx, j, s, c, result.key, func(current *session) {
			if current.Model.Images == nil {
				current.Model.Images = map[int64]string{}
			}
			if current.Model.Comments == nil {
				current.Model.Comments = map[int64]business.MusicComment{}
			}
			if result.isCover {
				current.Model.Images[result.id] = result.image
			} else {
				current.Model.Comments[result.id] = result.comment
			}
		}); e != nil {
			first = e
			cancel()
		}
	}
	if first != nil {
		return first
	}
	return p.patch(ctx, j, s, c, "enriched", func(current *session) { current.Model.Enriched = true })
}

func (p *Service) reply(ctx context.Context, j domain.Job, parent session, c continuation) error {
	id := domain.StableID(parent.ID, c.Event, c.Work)
	child, e := p.load(ctx, p.db, id, false)
	if e == nil {
		if child.State == "active" {
			return nil
		}
		if child.State == "sending" || child.State == "unknown" {
			return domain.ErrEffectUnknown
		}
	} else if !errors.Is(e, domain.ErrNotFound) {
		return e
	} else {
		child = parent
		child.ID = id
		child.Identity.ExecutionKey = "music-reply:" + c.Event
		child.Version = 1
		child.IntentVersion = 0
		child.Pending = nil
		child.Sequence = 1
		child.MessageID = ""
		child.State = "preparing"
		if c.Work == "album" {
			kind := parent.Request.Scene
			if parent.Request.Kind == "song" && parent.Model.Song != nil && parent.Model.Song.Item.AlbumID == c.ID {
				kind = "album"
			}
			if kind != "album" && kind != "playlist" {
				return domain.ErrUnauthorized
			}
			child.Request = Request{Kind: kind, ID: c.ID, Page: 1}
			if e = p.admit(ctx, child); e != nil {
				return e
			}
			if e = p.metadata(ctx, &child, false); e != nil {
				return safeResourceError(e)
			}
		}
	}
	if e = p.admit(ctx, child); e != nil {
		return e
	}
	var content string
	if c.Work == "lyrics" {
		if child.Model.Song == nil {
			return domain.ErrInvalid
		}
		content, e = p.renderLyrics(child)
	} else {
		content, e = p.render(child)
	}
	if e != nil {
		return e
	}
	f := frame{Sequence: 1, Version: 1, UUID: domain.StableUUID(id, "initial"), Content: content, Mode: "reply"}
	child.State = "sending"
	e = p.jobTx(ctx, j, func(tx *sql.Tx) error {
		if e := p.claimTx(ctx, tx, j); e != nil {
			return e
		}
		current, e := p.currentTx(ctx, tx, j, parent.ID)
		if e != nil {
			return e
		}
		if current.Version != c.Version {
			return domain.ErrConflict
		}
		if e = p.presentationGrant(ctx, tx, child, true); e != nil {
			return e
		}
		body, _ := json.Marshal(child)
		r, e := tx.ExecContext(ctx, `INSERT INTO `+p.table("music_card_sessions")+`(id,scope_key,body,version,sequence,state,expires_at) VALUES($1,$2,$3,1,1,'sending',$4) ON CONFLICT DO NOTHING`, id, child.Identity.Scope.Key(), body, child.Expires)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return domain.ErrEffectUnknown
		}
		return p.insertFrame(ctx, tx, child, f, "initial")
	})
	if e != nil {
		return e
	}
	if e = p.store.CheckClaim(ctx, j); e != nil {
		return e
	}
	if e = p.admit(ctx, child); e != nil {
		p.markInitial(ctx, child, "rejected", "", nil)
		return e
	}
	actual, e := p.messenger.Send(ctx, child.Identity.Scope, parent.MessageID, "interactive", f.Content, f.UUID)
	if e != nil || actual == "" {
		p.markInitial(context.WithoutCancel(ctx), child, "unknown", "", nil)
		return domain.ErrEffectUnknown
	}
	child.MessageID = actual
	child.State = "active"
	receiptCtx, receiptCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer receiptCancel()
	e = p.jobTx(receiptCtx, j, func(tx *sql.Tx) error {
		if e := p.claimTx(receiptCtx, tx, j); e != nil {
			return e
		}
		if e := p.save(receiptCtx, tx, child); e != nil {
			return e
		}
		if _, e := tx.ExecContext(receiptCtx, `UPDATE `+p.table("music_card_frames")+` SET state='sent',message_id=$2 WHERE session_id=$1 AND sequence=1`, id, actual); e != nil {
			return e
		}
		if c.Work == "album" {
			return p.enqueue(receiptCtx, tx, child, continuation{Work: "enrich", Actor: c.Actor, Event: c.Event})
		}
		return nil
	})
	if e != nil {
		return domain.ErrEffectUnknown
	}
	return nil
}

// OwnsMessage lets a local-first Messenger validate reply targets without
// querying a legacy store or granting access to another bot's cards.
func (p *Service) OwnsMessage(ctx context.Context, scope domain.Scope, id string) (bool, error) {
	if scope.Validate() != nil || id == "" {
		return false, domain.ErrInvalid
	}
	var exists bool
	e := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+p.table("music_card_sessions")+` WHERE scope_key=$1 AND message_id=$2 AND state='active' AND expires_at>clock_timestamp())`, scope.Key(), id).Scan(&exists)
	return exists, e
}
