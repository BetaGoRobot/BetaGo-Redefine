package musiccards

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards/view"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musicmedia"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type Media interface {
	ResolveCover(context.Context, domain.Scope, string, int64, string) (musicmedia.Cover, error)
	PrepareSong(context.Context, domain.Scope, int64, bool) (musicmedia.Song, error)
	UploadAudio(context.Context, domain.Scope, int64, bool) (musicmedia.Audio, error)
}
type Messenger interface {
	Send(context.Context, domain.Scope, string, string, string, string) (string, error)
	UpdateMusicCard(context.Context, domain.Scope, string, string, string, int) error
	Retract(context.Context, domain.Scope, string) error
}
type Options struct {
	DB         *sql.DB
	Schema     string
	Store      *pgstore.Store
	Provider   *business.MusicClient
	Media      Media
	Messenger  Messenger
	SigningKey []byte
	Admin      func(domain.Scope, string) bool
	Member     func(context.Context, domain.Scope, string) (bool, error)
}
type Service struct {
	db        *sql.DB
	schema    string
	store     *pgstore.Store
	provider  *business.MusicClient
	media     Media
	messenger Messenger
	key       []byte
	admin     func(domain.Scope, string) bool
	member    func(context.Context, domain.Scope, string) (bool, error)
}
type cardModel struct {
	Title    string
	Items    []business.MusicItem
	Total    int
	Images   map[int64]string
	Comments map[int64]business.MusicComment
	Song     *musicmedia.Song
	Audio    musicmedia.Audio
	Notice   string
	Enriched bool
}
type navigationIntent struct {
	Event, Actor, Work string
	BaseVersion        int64
	Request            Request
	Refresh            bool
}
type session struct {
	IntentVersion    int64
	Pending          *navigationIntent
	ID               string
	Identity         business.Identity
	Source           domain.SourceRef
	Request          Request
	Model            cardModel
	Version          int64
	Sequence         int
	State, MessageID string
	Expires          time.Time
}
type continuation struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Version int64  `json:"version"`
	Work    string `json:"work"`
	Event   string `json:"event,omitempty"`
	Actor   string `json:"actor,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
	ID      int64  `json:"id,omitempty"`
}
type frame struct {
	Sequence                              int
	Version                               int64
	UUID, Content, Mode, State, MessageID string
}

var schemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Store == nil || o.Provider == nil || o.Media == nil || o.Messenger == nil || !schemaPattern.MatchString(o.Schema) || len(o.SigningKey) < 32 {
		return nil, domain.ErrInvalid
	}
	return &Service{db: o.DB, schema: o.Schema, store: o.Store, provider: o.Provider, media: o.Media, messenger: o.Messenger, key: append([]byte(nil), o.SigningKey...), admin: o.Admin, member: o.Member}, nil
}
func (p *Service) table(name string) string { return `"` + p.schema + `"."` + name + `"` }
func (p *Service) Initialize(ctx context.Context) error {
	return p.tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "music-cards:"+p.schema); e != nil {
			return e
		}
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS ` + p.table("music_card_sessions") + `(id text PRIMARY KEY,scope_key text NOT NULL,body bytea NOT NULL,version bigint NOT NULL,sequence integer NOT NULL DEFAULT 0,state text NOT NULL,message_id text NOT NULL DEFAULT '',expires_at timestamptz NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp())`,
			`CREATE TABLE IF NOT EXISTS ` + p.table("music_card_frames") + `(session_id text NOT NULL REFERENCES ` + p.table("music_card_sessions") + `(id),sequence integer NOT NULL,view_version bigint NOT NULL,uuid text NOT NULL,content text NOT NULL,mode text NOT NULL,state text NOT NULL DEFAULT 'pending',message_id text NOT NULL DEFAULT '',work_key text NOT NULL,PRIMARY KEY(session_id,sequence),UNIQUE(session_id,work_key))`,
			`CREATE TABLE IF NOT EXISTS ` + p.table("music_card_events") + `(scope_key text NOT NULL,event_id text NOT NULL,digest text NOT NULL,session_id text NOT NULL,PRIMARY KEY(scope_key,event_id))`,
			`ALTER TABLE ` + p.table("music_card_sessions") + ` ADD COLUMN IF NOT EXISTS intent_version bigint NOT NULL DEFAULT 0`,
			`ALTER TABLE ` + p.table("music_card_events") + ` ADD COLUMN IF NOT EXISTS admitted_version bigint NOT NULL DEFAULT 0`,
		} {
			if _, e := tx.ExecContext(ctx, q); e != nil {
				return e
			}
		}
		return nil
	})
}
func (p *Service) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *Service) load(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, lock bool) (session, error) {
	var s session
	var body []byte
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	e := q.QueryRowContext(ctx, `SELECT body,version,sequence,state,message_id,intent_version FROM `+p.table("music_card_sessions")+` WHERE id=$1`+suffix, id).Scan(&body, &s.Version, &s.Sequence, &s.State, &s.MessageID, &s.IntentVersion)
	if errors.Is(e, sql.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	if e != nil {
		return s, e
	}
	version, sequence, state, msg, intent := s.Version, s.Sequence, s.State, s.MessageID, s.IntentVersion
	if e = json.Unmarshal(body, &s); e != nil {
		return s, e
	}
	s.Version, s.Sequence, s.State, s.MessageID, s.IntentVersion = version, sequence, state, msg, intent
	return s, nil
}
func (p *Service) save(ctx context.Context, tx *sql.Tx, s session) error {
	body, e := json.Marshal(s)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE `+p.table("music_card_sessions")+` SET body=$2,version=$3,sequence=$4,state=$5,message_id=$6,intent_version=$7,updated_at=clock_timestamp() WHERE id=$1`, s.ID, body, s.Version, s.Sequence, s.State, s.MessageID, s.IntentVersion)
	return e
}
func (p *Service) enqueue(ctx context.Context, tx *sql.Tx, s session, c continuation) error {
	c.Type = "typed_music"
	c.Session = s.ID
	if c.Version == 0 {
		c.Version = s.Version
	}
	body, e := json.Marshal(c)
	if e != nil {
		return e
	}
	scope, _ := json.Marshal(s.Identity.Scope)
	source, _ := json.Marshal(s.Source)
	key := domain.StableID(s.ID, strconv.FormatInt(c.Version, 10), c.Work, c.Event, strconv.FormatInt(c.ID, 10))
	_, e = tx.ExecContext(ctx, `INSERT INTO `+p.table("jobs")+`(scope_key,scope_body,source_body,kind,payload,dedupe_key,priority,max_attempts,available_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,350,8,clock_timestamp(),$7) ON CONFLICT(scope_key,kind,dedupe_key) DO NOTHING`, s.Identity.Scope.Key(), scope, source, domain.JobPresentation, body, "music:"+key, s.Expires)
	return e
}
func (p *Service) admit(ctx context.Context, s session) error {
	if time.Now().After(s.Expires) {
		return domain.ErrUnauthorized
	}
	if e := p.presentationGrant(ctx, p.db, s, false); e != nil {
		return e
	}
	a, e := p.store.CatalogAccess(ctx, s.Identity.Scope)
	if e != nil {
		return e
	}
	if a.Muted(time.Now()) || !a.FeatureEnabled("music") {
		return domain.ErrUnauthorized
	}
	m, e := p.store.Message(ctx, s.Identity.Scope, s.Source.MessageID)
	if e != nil {
		return e
	}
	if m.Deleted || m.Source != s.Source || m.Source.OccurredAt.Before(a.HistorySince) {
		return domain.ErrStaleSource
	}
	task, e := p.store.Task(ctx, s.Identity.Scope, s.Identity.TaskID)
	if e != nil {
		return e
	}
	if task.Status == "canceled" || !task.Authorized(s.Identity.ActorID) {
		return domain.ErrConflict
	}

	return nil
}
func (p *Service) presentationGrant(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, s session, lock bool) error {
	name := "music_send_card"
	if s.Request.Scene != "" {
		name = "music_send_search_card"
	}
	suffix := ""
	if lock {
		suffix = " FOR SHARE"
	}
	var allowed bool
	e := q.QueryRowContext(ctx, `SELECT enabled AND side_effect AND auto_execute FROM `+p.table("tool_policies")+` WHERE tool_name=$1`+suffix, name).Scan(&allowed)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !allowed {
		return domain.ErrUnauthorized
	}
	return e
}

func (p *Service) claimTx(ctx context.Context, tx *sql.Tx, j domain.Job) error {
	var id int64
	if e := tx.QueryRowContext(ctx, `SELECT id FROM `+p.table("jobs")+` WHERE id=$1 AND scope_key=$2 FOR UPDATE`, j.ID, j.Scope.Key()).Scan(&id); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return domain.ErrStaleClaim
		}
		return e
	}
	var valid bool
	e := tx.QueryRowContext(ctx, `SELECT owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp() FROM `+p.table("jobs")+` WHERE id=$1 AND scope_key=$2`, j.ID, j.Scope.Key(), j.Owner, j.Fence).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return domain.ErrStaleClaim
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, j.Scope.Key()+":"+j.Source.MessageID); e != nil {
		return e
	}
	var body []byte
	if e = tx.QueryRowContext(ctx, `SELECT body FROM `+p.table("messages")+` WHERE scope_key=$1 AND message_id=$2`, j.Scope.Key(), j.Source.MessageID).Scan(&body); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return domain.ErrStaleSource
		}
		return e
	}
	var m domain.Message
	if json.Unmarshal(body, &m) != nil || m.Deleted || m.Source != j.Source {
		return domain.ErrStaleSource
	}
	return p.recheckClaimTx(ctx, tx, j)
}

func (p *Service) recheckClaimTx(ctx context.Context, tx *sql.Tx, j domain.Job) error {
	var valid bool
	e := tx.QueryRowContext(ctx, `SELECT owner=$3 AND fence=$4 AND status='running' AND lease_until>clock_timestamp() AND expires_at>clock_timestamp() FROM `+p.table("jobs")+` WHERE id=$1 AND scope_key=$2`, j.ID, j.Scope.Key(), j.Owner, j.Fence).Scan(&valid)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !valid {
		return domain.ErrStaleClaim
	}
	return e
}
func (p *Service) currentTx(ctx context.Context, tx *sql.Tx, j domain.Job, id string) (session, error) {
	s, e := p.load(ctx, tx, id, true)
	if e != nil {
		return s, e
	}
	if e = p.presentationGrant(ctx, tx, s, true); e != nil {
		return s, e
	}
	return s, p.recheckClaimTx(ctx, tx, j)
}

func (p *Service) jobTx(ctx context.Context, j domain.Job, fn func(*sql.Tx) error) error {
	return p.tx(ctx, func(tx *sql.Tx) error {
		if e := p.claimTx(ctx, tx, j); e != nil {
			return e
		}
		if e := fn(tx); e != nil {
			return e
		}
		return p.recheckClaimTx(ctx, tx, j)
	})
}

// Send reserves one effect identity before any provider send. A missing send
// receipt is terminal uncertainty; retrying the caller cannot create a duplicate.
func (p *Service) Send(ctx context.Context, in business.Identity, parent string, r Request) (Receipt, error) {
	if in.Scope.Validate() != nil || in.ActorID == "" || in.TaskID == "" || in.ExecutionKey == "" || parent == "" {
		return Receipt{State: "rejected"}, domain.ErrInvalid
	}
	r, e := normalizeRequest(r)
	if e != nil {
		return Receipt{State: "rejected"}, e
	}
	encoded, _ := json.Marshal(r)
	id := domain.StableID("music", in.Scope.Key(), in.TaskID, in.ExecutionKey, parent, string(encoded))
	s := session{ID: id, Identity: in, Request: r, Version: 1, State: "preparing", Expires: time.Now().UTC().Add(7 * 24 * time.Hour)}
	old, e := p.load(ctx, p.db, id, false)
	if e == nil {
		s = old
		if s.State == "active" {
			return receipt(s), nil
		}
		if s.State == "sending" || s.State == "unknown" {
			return receipt(s), domain.ErrEffectUnknown
		}
		if s.State == "withdrawn" {
			return receipt(s), domain.ErrConflict
		}
	} else if !errors.Is(e, domain.ErrNotFound) {
		return Receipt{State: "rejected"}, e
	}
	m, e := p.store.Message(ctx, in.Scope, parent)
	if e != nil || m.Deleted {
		if e == nil {
			e = domain.ErrStaleSource
		}
		return Receipt{State: "rejected"}, e
	}
	s.Source = m.Source
	if e = p.admit(ctx, s); e != nil {
		return Receipt{State: "rejected"}, e
	}
	body, _ := json.Marshal(s)
	_, e = p.db.ExecContext(ctx, `INSERT INTO `+p.table("music_card_sessions")+`(id,scope_key,body,version,state,expires_at) VALUES($1,$2,$3,1,'preparing',$4) ON CONFLICT DO NOTHING`, s.ID, in.Scope.Key(), body, s.Expires)
	if e != nil {
		return Receipt{State: "rejected"}, e
	}
	if e = p.metadata(ctx, &s, false); e != nil {
		return Receipt{SessionID: id, State: "rejected"}, safeResourceError(e)
	}
	var initial frame
	e = p.tx(ctx, func(tx *sql.Tx) error {
		current, e := p.load(ctx, tx, id, true)
		if e != nil {
			return e
		}
		if current.State != "preparing" {
			s = current
			return domain.ErrConflict
		}
		if e = p.presentationGrant(ctx, tx, s, true); e != nil {
			return e
		}
		s.Sequence = 1
		s.State = "sending"
		content, e := p.render(s)
		if e != nil {
			return e
		}
		initial = frame{Sequence: 1, Version: 1, UUID: domain.StableUUID(id, "initial"), Content: content, Mode: "initial"}
		if e = p.insertFrame(ctx, tx, s, initial, "initial"); e != nil {
			return e
		}
		return p.save(ctx, tx, s)
	})
	if e != nil {
		if s.State == "active" {
			return receipt(s), nil
		}
		if s.State == "sending" || s.State == "unknown" {
			return receipt(s), domain.ErrEffectUnknown
		}
		return Receipt{SessionID: id, State: "rejected"}, e
	}
	if e = p.admit(ctx, s); e != nil {
		p.markInitial(ctx, s, "rejected", "", e)
		return Receipt{SessionID: id, State: "rejected"}, e
	}
	actual, e := p.messenger.Send(ctx, in.Scope, parent, "interactive", initial.Content, initial.UUID)
	if e != nil || actual == "" {
		p.markInitial(context.WithoutCancel(ctx), s, "unknown", "", nil)
		s.State = "unknown"
		return receipt(s), domain.ErrEffectUnknown
	}
	s.State = "active"
	s.MessageID = actual
	receiptCtx, receiptCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer receiptCancel()
	e = p.tx(receiptCtx, func(tx *sql.Tx) error {
		current, e := p.load(receiptCtx, tx, id, true)
		if e != nil {
			return e
		}
		if current.State != "sending" {
			return domain.ErrConflict
		}
		if e = p.save(receiptCtx, tx, s); e != nil {
			return e
		}
		if _, e = tx.ExecContext(receiptCtx, `UPDATE `+p.table("music_card_frames")+` SET state='sent',message_id=$2 WHERE session_id=$1 AND sequence=1`, id, actual); e != nil {
			return e
		}
		work := "enrich"
		if r.Kind == "song" {
			work = "audio"
		}
		return p.enqueue(receiptCtx, tx, s, continuation{Work: work})
	})
	if e != nil {
		return Receipt{SessionID: id, State: "unknown"}, domain.ErrEffectUnknown
	}
	return receipt(s), nil
}
func receipt(s session) Receipt {
	return Receipt{MessageID: s.MessageID, SessionID: s.ID, State: s.State}
}
func safeResourceError(e error) error {
	if errors.Is(e, domain.ErrInvalid) || errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrUnauthorized) || errors.Is(e, domain.ErrStaleSource) {
		return e
	}
	return business.ErrProvider
}
func (p *Service) markInitial(ctx context.Context, s session, state, id string, _ error) {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = p.tx(bounded, func(tx *sql.Tx) error {
		current, e := p.load(bounded, tx, s.ID, true)
		if e != nil {
			return e
		}
		if current.State != "sending" {
			return nil
		}
		current.State = state
		current.MessageID = id
		return p.save(bounded, tx, current)
	})
}
func (p *Service) metadata(ctx context.Context, s *session, refresh bool) error {
	r := s.Request
	if r.Kind == "song" {
		song, e := p.media.PrepareSong(ctx, s.Identity.Scope, r.ID, refresh)
		if e != nil {
			return e
		}
		if song.Item.ID != r.ID {
			return domain.ErrConflict
		}
		s.Model = cardModel{Title: song.Item.Name, Items: []business.MusicItem{song.Item}, Total: 1, Song: &song}
		return nil
	}
	model := cardModel{Images: map[int64]string{}, Comments: map[int64]business.MusicComment{}}
	if r.Kind != "" {
		result, e := p.provider.Detail(ctx, r.Kind, r.ID, 5, (r.Page-1)*5)
		if e != nil {
			return e
		}
		model.Title = result.Name
		model.Items = result.Tracks
		model.Total = result.Total
	} else {
		result, e := p.provider.Search(ctx, business.MusicSearch{Kind: r.Scene, Keywords: r.Query, Limit: 5, Offset: (r.Page - 1) * 5})
		if e != nil {
			return e
		}
		model.Title = r.Query
		model.Items = result.Items
		model.Total = result.Total
	}
	s.Model = model
	return nil
}
func (p *Service) action(s session, action, text string, id int64, page int) view.Action {
	c := actionClaims{Session: s.ID, Scope: s.Identity.Scope, Version: s.Version, Action: action, ID: id, Page: page, Expires: s.Expires.Unix()}
	return view.Action{Text: text, Value: map[string]any{"kind": "music", "token": signAction(p.key, c)}}
}
func (p *Service) render(s session) (string, error) {
	refresh := p.action(s, "refresh", "刷新", 0, 0)
	withdraw := p.action(s, "withdraw", "撤回", 0, 0)
	withdraw.ConfirmTitle = "撤回音乐卡片"
	withdraw.ConfirmText = "仅发起人或管理员可以撤回，确认继续？"
	if s.Request.Kind == "song" && s.Model.Song != nil {
		m := s.Model.Song
		var album view.Action
		if m.Item.AlbumID > 0 {
			album = p.action(s, "album", "查看专辑", m.Item.AlbumID, 0)
		}
		return view.RenderDetail(view.Detail{Title: m.Item.Name, Subtitle: strings.Join(m.Item.Artists, " / "), ImageKey: m.ImageKey, Lyrics: lyricExcerpt(m.Lyrics.Full), PlayerURL: m.PlayerURL, SourceURL: fmt.Sprintf("https://music.163.com/#/song?id=%d", m.Item.ID), AudioFileKey: s.Model.Audio.FileKey, AudioID: strconv.FormatInt(m.Item.ID, 10), DurationMS: int64(s.Model.Audio.DurationMS), FullLyricsAction: p.action(s, "lyrics", "完整歌词", m.Item.ID, 0), AlbumAction: album, Refresh: refresh, Withdraw: withdraw, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Notice: s.Model.Notice})
	}
	totalPages := min(2001, max(1, (s.Model.Total+4)/5))
	list := view.List{Title: s.Model.Title, Page: s.Request.Page, TotalPages: totalPages, Total: s.Model.Total, Prev: p.action(s, "page", "上一页", 0, s.Request.Page-1), Next: p.action(s, "page", "下一页", 0, s.Request.Page+1), Refresh: refresh, Withdraw: withdraw, Notice: s.Model.Notice, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	list.Prev.Disabled = s.Request.Page <= 1
	list.Next.Disabled = s.Request.Page >= totalPages
	kind := s.Request.Scene
	if s.Request.Kind != "" {
		kind = "song"
	}
	for _, item := range s.Model.Items {
		action := "play"
		label := "播放"
		if kind != "song" {
			action = "album"
			label = "查看专辑"
			if kind == "playlist" {
				label = "查看歌单"
			}
		}
		comment := s.Model.Comments[item.ID]
		text := excerpt(comment.Content, 300)
		if comment.Author != "" {
			text += " — " + comment.Author
		}
		_, imageDone := s.Model.Images[item.ID]
		_, commentDone := s.Model.Comments[item.ID]
		row := view.Item{ID: strconv.FormatInt(item.ID, 10), Title: item.Name, Artist: strings.Join(item.Artists, " / "), ImageKey: s.Model.Images[item.ID], Comment: text, ImageLoading: !s.Model.Enriched && !imageDone, CommentLoading: !s.Model.Enriched && !commentDone, Action: p.action(s, action, label, item.ID, 0)}
		if comment.TimeMS > 0 {
			row.CommentTime = time.UnixMilli(comment.TimeMS).Format("2006-01-02")
		}
		list.Items = append(list.Items, row)
	}
	return view.RenderList(list)
}
func (p *Service) insertFrame(ctx context.Context, tx *sql.Tx, s session, f frame, workKey string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO `+p.table("music_card_frames")+`(session_id,sequence,view_version,uuid,content,mode,work_key) VALUES($1,$2,$3,$4,$5,$6,$7)`, s.ID, f.Sequence, f.Version, f.UUID, f.Content, f.Mode, workKey)
	return e
}

func (p *Service) renderLyrics(s session) (string, error) {
	m := s.Model.Song
	if m == nil {
		return "", domain.ErrInvalid
	}
	withdraw := p.action(s, "withdraw", "撤回", 0, 0)
	return view.RenderLyrics(view.Lyrics{Title: m.Item.Name, Subtitle: strings.Join(m.Item.Artists, " / "), Text: m.Lyrics.Full, ImageKey: m.ImageKey, DownloadURL: m.LyricsURL, Withdraw: withdraw, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
}

func (p *Service) CheckMessage(ctx context.Context, scope domain.Scope, id string) error {
	ok, e := p.OwnsMessage(ctx, scope, id)
	if e != nil {
		return e
	}
	if !ok {
		return domain.ErrUnauthorized
	}
	return nil
}

func lyricExcerpt(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) > 12 {
		lines = append(lines[:12], "…")
	}
	return excerpt(strings.Join(lines, "\n"), 1800)
}
func excerpt(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return text
}
