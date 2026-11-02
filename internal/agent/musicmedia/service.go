// Package musicmedia publishes and uploads provider music in the independent
// agent's own namespace. Provider objects never impersonate Lark originals.
package musicmedia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/media"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type ImageUploader interface {
	UploadMusicImage(context.Context, domain.Scope, []byte) (string, error)
}
type AudioUploader interface {
	UploadBytes(context.Context, domain.Scope, business.AudioBytes) (business.AudioAsset, error)
}
type Config struct {
	DB                                   *sql.DB
	Schema                               string
	Provider                             *business.MusicClient
	ObjectStore                          media.MinIOConfig
	PublicURL, PlayerURL                 string
	SigningKey                           []byte
	ImageUploader                        ImageUploader
	AudioUploader                        AudioUploader
	AllowedMediaHosts, AllowedImageHosts []string
	HTTPClient                           *http.Client
	AssetTTL                             time.Duration
}
type Cover struct{ ImageKey, URL string }
type Song struct {
	Item                                                              business.MusicItem
	Lyrics                                                            business.MusicLyrics
	ImageKey, PictureURL, LyricsURL, MusicURL, ManifestURL, PlayerURL string
}
type Audio = business.AudioAsset

type Service struct {
	cfg          Config
	objects      *minio.Client
	http         *http.Client
	playerOrigin string
}
type coverRecord struct{ ImageKey, ObjectKey string }
type sourceRecord struct {
	Item                         business.MusicItem
	Lyrics                       business.MusicLyrics
	Cover                        coverRecord
	MusicKey, LyricsKey, Version string
}

var ErrAudioUnavailable = errors.New("music native audio unavailable")
var ErrUploadUnknown = errors.New("music asset upload outcome unknown; explicit refresh required")
var errStorage = errors.New("music asset storage unavailable")
var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func New(cfg Config) (*Service, error) {
	if cfg.DB == nil || !schemaName.MatchString(cfg.Schema) || cfg.Provider == nil || cfg.ImageUploader == nil || len(cfg.SigningKey) < 32 {
		return nil, domain.ErrInvalid
	}
	public, e := url.Parse(cfg.PublicURL)
	if e != nil || !validURL(public) || public.RawQuery != "" || (public.Path != "" && public.Path != "/") {
		return nil, domain.ErrInvalid
	}
	player, e := url.Parse(cfg.PlayerURL)
	if e != nil || !validURL(player) {
		return nil, domain.ErrInvalid
	}
	if cfg.AssetTTL == 0 {
		cfg.AssetTTL = 7 * 24 * time.Hour
	}
	if cfg.AssetTTL < time.Minute || cfg.AssetTTL > 7*24*time.Hour {
		return nil, domain.ErrInvalid
	}
	cfg.SigningKey = append([]byte(nil), cfg.SigningKey...)
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	if len(cfg.AllowedMediaHosts) == 0 {
		cfg.AllowedMediaHosts = business.DefaultMusicMediaHosts()
	}
	if len(cfg.AllowedImageHosts) == 0 {
		cfg.AllowedImageHosts = []string{"music.126.net", "music.163.com"}
	}
	cfg.AllowedMediaHosts = append([]string(nil), cfg.AllowedMediaHosts...)
	cfg.AllowedImageHosts = append([]string(nil), cfg.AllowedImageHosts...)
	for _, host := range append(append([]string(nil), cfg.AllowedMediaHosts...), cfg.AllowedImageHosts...) {
		if host == "" || strings.ContainsAny(host, "/:@\\ \r\n") {
			return nil, domain.ErrInvalid
		}
	}
	object := cfg.ObjectStore
	if object.Endpoint == "" || object.Bucket == "" || object.AccessKey == "" || object.SecretKey == "" {
		return nil, domain.ErrInvalid
	}
	if object.MaxBytes == 0 {
		object.MaxBytes = 32 << 20
	}
	if object.Timeout == 0 {
		object.Timeout = time.Minute
	}
	if object.MaxBytes < 1 || object.MaxBytes > 32<<20 || object.Timeout <= 0 || object.Timeout > 5*time.Minute {
		return nil, domain.ErrInvalid
	}
	cfg.ObjectStore = object
	objects, e := minio.New(object.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(object.AccessKey, object.SecretKey, ""), Secure: object.Secure})
	if e != nil {
		return nil, domain.ErrInvalid
	}
	hc := http.Client{}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}
	hc.Jar = nil
	hc.Timeout = 2 * time.Minute
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Service{cfg: cfg, objects: objects, http: &hc, playerOrigin: player.Scheme + "://" + player.Host}, nil
}
func validURL(u *url.URL) bool {
	return u != nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http")
}
func (s *Service) table(name string) string { return `"` + s.cfg.Schema + `"."music_` + name + `"` }

// Initialize touches only this service's dedicated schema tables and configured
// bucket. It does not inspect or delete the legacy music cache.
func (s *Service) Initialize(ctx context.Context) error {
	tx, e := s.cfg.DB.BeginTx(ctx, nil)
	if e != nil {
		return errStorage
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "music-migrate:"+s.cfg.Schema); e != nil {
		return errStorage
	}
	for _, q := range []string{
		`CREATE SCHEMA IF NOT EXISTS "` + s.cfg.Schema + `"`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("objects") + ` (object_key text PRIMARY KEY,scope_key text NOT NULL,purpose text NOT NULL,mime text NOT NULL,size bigint NOT NULL CHECK(size>0),digest text NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp())`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("sources") + ` (cache_key text PRIMARY KEY,body bytea NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp())`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("uploads") + ` (cache_key text PRIMARY KEY,status text NOT NULL CHECK(status IN ('unknown','complete')),body bytea NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp())`,
	} {
		if _, e = tx.ExecContext(ctx, q); e != nil {
			return errStorage
		}
	}
	if e = tx.Commit(); e != nil {
		return errStorage
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ObjectStore.Timeout)
	defer cancel()
	exists, e := s.objects.BucketExists(ctx, s.cfg.ObjectStore.Bucket)
	if e != nil {
		return errStorage
	}
	if !exists {
		if e = s.objects.MakeBucket(ctx, s.cfg.ObjectStore.Bucket, minio.MakeBucketOptions{}); e != nil {
			switch minio.ToErrorResponse(e).Code {
			case "BucketAlreadyOwnedByYou":
				// A concurrent initializer created this same owner's bucket.
			case "BucketAlreadyExists":
				// This code alone does not establish ownership or access.
				accessible, checkErr := s.objects.BucketExists(ctx, s.cfg.ObjectStore.Bucket)
				if checkErr != nil || !accessible {
					return errStorage
				}
			default:
				return errStorage
			}
		}
	}
	return nil
}

type connectionKey struct{}
type cacheDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) db(ctx context.Context) cacheDB {
	if conn, ok := ctx.Value(connectionKey{}).(*sql.Conn); ok {
		return conn
	}
	return s.cfg.DB
}

// A scoped SQL connection holds a session advisory lock and commits upload
// admission before network calls. Nested source/cover operations share this same
// connection, so a small pool cannot deadlock waiting for its own lease.
func (s *Service) locked(ctx context.Context, key string, f func(context.Context) error) error {
	conn, shared := ctx.Value(connectionKey{}).(*sql.Conn)
	if !shared {
		var e error
		conn, e = s.cfg.DB.Conn(ctx)
		if e != nil {
			return errStorage
		}
		defer conn.Close()
		ctx = context.WithValue(ctx, connectionKey{}, conn)
	}
	lock := s.cfg.Schema + ":" + key
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(release, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lock); e != nil {
			// A connection with an unverified session lock must not return to the pool.
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, e := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, lock); e != nil {
		return errStorage
	}
	return f(ctx)
}
func (s *Service) upload(ctx context.Context, key string, refresh bool, work func(context.Context) ([]byte, error)) ([]byte, error) {
	return s.preparedUpload(ctx, key, refresh, func(context.Context) (func(context.Context) ([]byte, error), error) {
		return work, nil
	})
}

// Preparation stays under the same scoped lock, but cannot leave an unknown
// external upload receipt: no provider upload has been attempted at that point.
func (s *Service) preparedUpload(ctx context.Context, key string, refresh bool, prepare func(context.Context) (func(context.Context) ([]byte, error), error)) ([]byte, error) {
	var out []byte
	err := s.locked(ctx, "upload:"+key, func(ctx context.Context) error {
		var state string
		var body []byte
		e := s.db(ctx).QueryRowContext(ctx, `SELECT status,body FROM `+s.table("uploads")+` WHERE cache_key=$1`, key).Scan(&state, &body)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return errStorage
		}
		if e == nil && !refresh {
			if state != "complete" {
				return ErrUploadUnknown
			}
			out = body
			return nil
		}
		work, e := prepare(ctx)
		if e != nil {
			return e
		}
		if _, e = s.db(ctx).ExecContext(ctx, `INSERT INTO `+s.table("uploads")+` (cache_key,status,body) VALUES($1,'unknown',$2) ON CONFLICT(cache_key) DO UPDATE SET status='unknown',body=EXCLUDED.body,updated_at=clock_timestamp()`, key, []byte{}); e != nil {
			return errStorage
		}
		body, e = work(ctx)
		if e != nil {
			return e
		}
		if _, e = s.db(ctx).ExecContext(ctx, `UPDATE `+s.table("uploads")+` SET status='complete',body=$2,updated_at=clock_timestamp() WHERE cache_key=$1`, key, body); e != nil {
			return errStorage
		}
		out = body
		return nil
	})
	return out, err
}
func (s *Service) download(ctx context.Context, raw string, hosts []string, max int64) ([]byte, string, error) {
	u, e := business.ValidateMusicMediaURL(raw, hosts)
	if e != nil {
		return nil, "", e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return nil, "", business.ErrProvider
	}
	resp, e := s.http.Do(req)
	if e != nil {
		return nil, "", business.ErrProvider
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.ContentLength > max {
		return nil, "", business.ErrProvider
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if e != nil || len(b) == 0 || int64(len(b)) > max {
		return nil, "", business.ErrProvider
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(b)
	}
	return b, mime, nil
}
func (s *Service) put(ctx context.Context, scope domain.Scope, purpose, mime string, b []byte) (string, error) {
	if len(b) == 0 || int64(len(b)) > s.cfg.ObjectStore.MaxBytes {
		return "", domain.ErrInvalid
	}
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	key := "music/v1/" + scope.Key() + "/" + purpose + "/" + digest
	opts := minio.PutObjectOptions{ContentType: mime, UserMetadata: map[string]string{"scope": scope.Key(), "purpose": purpose, "sha256": digest}, DisableMultipart: true}
	opts.SetMatchETagExcept("*")
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ObjectStore.Timeout)
	defer cancel()
	_, e := s.objects.PutObject(ctx, s.cfg.ObjectStore.Bucket, key, bytes.NewReader(b), int64(len(b)), opts)
	if e != nil && minio.ToErrorResponse(e).Code != "PreconditionFailed" {
		return "", errStorage
	}
	if e != nil {
		info, err := s.objects.StatObject(ctx, s.cfg.ObjectStore.Bucket, key, minio.StatObjectOptions{})
		if err != nil || info.Size != int64(len(b)) || info.UserMetadata["Sha256"] != digest {
			return "", errStorage
		}
	}
	if _, e = s.db(ctx).ExecContext(ctx, `INSERT INTO `+s.table("objects")+` (object_key,scope_key,purpose,mime,size,digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(object_key) DO NOTHING`, key, scope.Key(), purpose, mime, len(b), digest); e != nil {
		return "", errStorage
	}
	return key, nil
}
func (s *Service) resolveCover(ctx context.Context, scope domain.Scope, kind string, id int64, raw string, refresh bool) (coverRecord, error) {
	if scope.Validate() != nil || scope.Visibility != "group" || id <= 0 || (kind != "song" && kind != "album" && kind != "playlist") {
		return coverRecord{}, domain.ErrInvalid
	}
	if _, e := business.ValidateMusicMediaURL(raw, s.cfg.AllowedImageHosts); e != nil {
		return coverRecord{}, e
	}
	key := domain.StableID("music-cover-v1", scope.Key(), s.cfg.Provider.Fingerprint(), kind, strconv.FormatInt(id, 10), raw)
	body, e := s.preparedUpload(ctx, key, refresh, func(ctx context.Context) (func(context.Context) ([]byte, error), error) {
		b, _, e := s.download(ctx, raw, s.cfg.AllowedImageHosts, min(s.cfg.ObjectStore.MaxBytes, 10<<20))
		if e != nil {
			return nil, e
		}
		// CDN headers may use aliases such as image/jpg, or mislabel an error
		// document as an image. Identify the bytes before storing or uploading.
		mime := http.DetectContentType(b)
		switch mime {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return nil, domain.ErrInvalid
		}
		object, e := s.put(ctx, scope, "cover", mime, b)
		if e != nil {
			return nil, e
		}
		return func(ctx context.Context) ([]byte, error) {
			image, e := s.cfg.ImageUploader.UploadMusicImage(ctx, scope, b)
			if e != nil {
				return nil, e
			}
			if strings.TrimSpace(image) == "" {
				return nil, business.ErrProvider
			}
			return json.Marshal(coverRecord{ImageKey: image, ObjectKey: object})
		}, nil
	})
	if e != nil {
		return coverRecord{}, e
	}
	var out coverRecord
	if json.Unmarshal(body, &out) != nil || out.ImageKey == "" || out.ObjectKey == "" {
		return coverRecord{}, errStorage
	}
	return out, nil
}
func (s *Service) ResolveCover(ctx context.Context, scope domain.Scope, kind string, id int64, raw string) (Cover, error) {
	cover, e := s.resolveCover(ctx, scope, kind, id, raw, false)
	if e != nil {
		return Cover{}, e
	}
	return Cover{ImageKey: cover.ImageKey, URL: s.assetURL(cover.ObjectKey, time.Now().Add(s.cfg.AssetTTL))}, nil
}

// RefreshCover is an explicit user-authorized retry of a selected cover. It
// permits recovery from an unknown receipt without letting ordinary list
// enrichment repeat a potentially completed Lark upload.
func (s *Service) RefreshCover(ctx context.Context, scope domain.Scope, kind string, id int64, raw string) (Cover, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cover, e := s.resolveCover(ctx, scope, kind, id, raw, true)
	if e != nil {
		return Cover{}, e
	}
	return Cover{ImageKey: cover.ImageKey, URL: s.assetURL(cover.ObjectKey, time.Now().Add(s.cfg.AssetTTL))}, nil
}

func (s *Service) source(ctx context.Context, scope domain.Scope, id int64, refresh bool) (sourceRecord, error) {
	if scope.Validate() != nil || scope.Visibility != "group" || id <= 0 {
		return sourceRecord{}, domain.ErrInvalid
	}
	key := domain.StableID("music-source-v1", scope.Key(), s.cfg.Provider.Fingerprint(), strconv.FormatInt(id, 10))
	var out sourceRecord
	err := s.locked(ctx, "source:"+key, func(ctx context.Context) error {
		if !refresh {
			var b []byte
			e := s.db(ctx).QueryRowContext(ctx, `SELECT body FROM `+s.table("sources")+` WHERE cache_key=$1`, key).Scan(&b)
			if e == nil {
				if json.Unmarshal(b, &out) != nil {
					return errStorage
				}
				return nil
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return errStorage
			}
		}
		detail, e := s.cfg.Provider.Detail(ctx, "song", id, 1, 0)
		if e != nil {
			return e
		}
		if len(detail.Tracks) != 1 || detail.Tracks[0].ID != id {
			return domain.ErrNotFound
		}
		out.Item = detail.Tracks[0]
		out.Lyrics, e = s.cfg.Provider.Lyrics(ctx, id)
		if e != nil {
			return e
		}
		raw, e := s.cfg.Provider.PlaybackURL(ctx, id, business.PlaybackOptions{Refresh: refresh})
		if e != nil {
			return e
		}
		b, mime, e := s.download(ctx, raw, s.cfg.AllowedMediaHosts, s.cfg.ObjectStore.MaxBytes)
		if e != nil {
			return e
		}
		if !strings.HasPrefix(mime, "audio/") && mime != "application/ogg" && mime != "application/octet-stream" {
			return domain.ErrInvalid
		}
		out.MusicKey, e = s.put(ctx, scope, "audio", mime, b)
		if e != nil {
			return e
		}
		out.Cover, e = s.resolveCover(ctx, scope, "song", id, out.Item.ImageURL, refresh)
		if e != nil {
			return e
		}
		lrc := out.Lyrics.Original
		if lrc == "" {
			lrc = out.Lyrics.Full
		}
		if lrc == "" {
			lrc = "[00:00.00]"
		}
		out.LyricsKey, e = s.put(ctx, scope, "lyrics", "text/plain; charset=utf-8", []byte(lrc))
		if e != nil {
			return e
		}
		item, _ := json.Marshal(out.Item)
		out.Version = domain.StableID(out.MusicKey, out.Cover.ObjectKey, out.LyricsKey, string(item))
		body, _ := json.Marshal(out)
		if _, e = s.db(ctx).ExecContext(ctx, `INSERT INTO `+s.table("sources")+` (cache_key,body) VALUES($1,$2) ON CONFLICT(cache_key) DO UPDATE SET body=EXCLUDED.body,updated_at=clock_timestamp()`, key, body); e != nil {
			return errStorage
		}
		return nil
	})
	return out, err
}
func (s *Service) PrepareSong(ctx context.Context, scope domain.Scope, id int64, refresh bool) (Song, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	source, e := s.source(ctx, scope, id, refresh)
	if e != nil {
		return Song{}, e
	}
	expiry := time.Now().Add(s.cfg.AssetTTL)
	out := Song{Item: source.Item, Lyrics: source.Lyrics, ImageKey: source.Cover.ImageKey, PictureURL: s.assetURL(source.Cover.ObjectKey, expiry), LyricsURL: s.assetURL(source.LyricsKey, expiry), MusicURL: s.assetURL(source.MusicKey, expiry)}
	type artist struct {
		Name string `json:"name"`
	}
	manifest := struct {
		Title, LyricsURL, MusicURL, PictureURL, Album string
		Artist                                        []artist
		Duration                                      int
	}{Title: out.Item.Name, LyricsURL: out.LyricsURL, MusicURL: out.MusicURL, PictureURL: out.PictureURL, Album: out.Item.Album, Duration: out.Item.DurationMS, Artist: []artist{}}
	for _, name := range out.Item.Artists {
		manifest.Artist = append(manifest.Artist, artist{Name: name})
	}
	b, _ := json.Marshal(manifest)
	key, e := s.put(ctx, scope, "manifest", "application/json; charset=utf-8", b)
	if e != nil {
		return Song{}, e
	}
	out.ManifestURL = s.assetURL(key, expiry)
	player, _ := url.Parse(s.cfg.PlayerURL)
	q := player.Query()
	q.Set("target", out.ManifestURL)
	player.RawQuery = q.Encode()
	out.PlayerURL = player.String()
	return out, nil
}
func (s *Service) UploadAudio(ctx context.Context, scope domain.Scope, id int64, refresh bool) (Audio, error) {
	return s.uploadAudio(ctx, scope, id, refresh, refresh)
}

// RefreshAudio forces a new native receipt for the currently prepared source.
// A presentation refresh prepares its source first, then calls this method to
// recover complete or unknown upload receipts without refetching music/covers.
func (s *Service) RefreshAudio(ctx context.Context, scope domain.Scope, id int64) (Audio, error) {
	return s.uploadAudio(ctx, scope, id, false, true)
}
func (s *Service) uploadAudio(ctx context.Context, scope domain.Scope, id int64, sourceRefresh, uploadRefresh bool) (Audio, error) {
	if s.cfg.AudioUploader == nil {
		return Audio{}, ErrAudioUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// Source resolution and native receipt refresh are independently controlled.
	source, e := s.source(ctx, scope, id, sourceRefresh)
	if e != nil {
		return Audio{}, e
	}
	fingerprint := "scoped-uploader-v1"
	if f, ok := s.cfg.AudioUploader.(interface{ Fingerprint() string }); ok {
		fingerprint = f.Fingerprint()
	}
	key := domain.StableID("music-audio-upload-v1", scope.Key(), source.Version, fingerprint)
	body, e := s.upload(ctx, key, uploadRefresh, func(ctx context.Context) ([]byte, error) {
		object, e := s.objects.GetObject(ctx, s.cfg.ObjectStore.Bucket, source.MusicKey, minio.GetObjectOptions{})
		if e != nil {
			return nil, errStorage
		}
		defer object.Close()
		b, e := io.ReadAll(io.LimitReader(object, s.cfg.ObjectStore.MaxBytes+1))
		if e != nil || len(b) == 0 || int64(len(b)) > s.cfg.ObjectStore.MaxBytes {
			return nil, errStorage
		}
		asset, e := s.cfg.AudioUploader.UploadBytes(ctx, scope, business.AudioBytes{SongID: id, Name: source.Item.Name, Data: b})
		if e != nil {
			return nil, e
		}
		if strings.TrimSpace(asset.FileKey) == "" || asset.DurationMS <= 0 {
			return nil, business.ErrProvider
		}
		return json.Marshal(asset)
	})
	if e != nil {
		return Audio{}, e
	}
	var out Audio
	if json.Unmarshal(body, &out) != nil || out.FileKey == "" || out.DurationMS <= 0 {
		return Audio{}, errStorage
	}
	return out, nil
}
