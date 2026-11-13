package music

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

type Options struct {
	DBProvider    func() *gorm.DB
	Provider      Provider
	Verifier      IdentityVerifier
	AppID         string
	Origin        string
	Addr          string
	StaticDir     string
	SecureCookie  bool
	SkipMigration bool
	Now           func() time.Time
	providerErr   error
}
type Server struct {
	db          *gorm.DB
	opts        Options
	authOptions AuthOptions
	mu          sync.Mutex
	rates       map[string]bucket
}

// AuthOptions is public capability discovery and contains no credential or identity.
type AuthOptions struct {
	Guest  bool              `json:"guest"`
	Feishu FeishuAuthOptions `json:"feishu"`
}
type FeishuAuthOptions struct {
	Enabled bool   `json:"enabled"`
	AppID   string `json:"appID"`
}
type bucket struct {
	at time.Time
	n  int
}
type account struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	ExpiresAt time.Time `json:"-"`
}

func NewServer(db *gorm.DB, opts Options) *Server {
	capabilities := AuthOptions{Guest: true}
	if opts.Verifier != nil && strings.TrimSpace(opts.AppID) != "" {
		capabilities.Feishu = FeishuAuthOptions{Enabled: true, AppID: opts.AppID}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.AppID == "" {
		opts.AppID = "music"
	}
	return &Server{db: uncached(db), opts: opts, authOptions: capabilities, rates: map[string]bucket{}}
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func body(r *http.Request, v any) error {
	if r.Body == nil {
		return invalid("json_body_required")
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024+1))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return invalid("invalid_json")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return invalid("invalid_json")
	}
	return nil
}
func fail(w http.ResponseWriter, err error) {
	var e *apiError
	if errors.As(err, &e) {
		write(w, e.Status, map[string]string{"error": e.Message})
	} else {
		write(w, 500, map[string]string{"error": "internal_error"})
	}
}
func (s *Server) limited(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a, err := s.auth(r); err == nil {
		host = "user:" + a.ID
	}
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.rates[host]
	if now.Sub(b.at) > time.Minute {
		b = bucket{at: now}
	}
	b.n++
	s.rates[host] = b
	if len(s.rates) > 10000 {
		for k, v := range s.rates {
			if now.Sub(v.at) > time.Minute {
				delete(s.rates, k)
			}
		}
	}
	return b.n > 180
}
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if s.limited(r) {
			write(w, 429, map[string]string{"error": "rate_limited"})
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != s.opts.Origin {
				write(w, 403, map[string]string{"error": "origin_denied"})
				return
			}
			if r.Method != "DELETE" {
				mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if mt != "application/json" {
					write(w, 415, map[string]string{"error": "json_required"})
					return
				}
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
		out, err := s.route(r)
		if err != nil {
			fail(w, err)
			return
		}
		if v, ok := out.(*sessionResult); ok && v != nil && v.SessionToken == "" {
			http.SetCookie(w, &http.Cookie{Name: "music_session", Value: v.cookie, Path: "/music-api/v1", HttpOnly: true, Secure: s.opts.SecureCookie, SameSite: http.SameSiteLaxMode, Expires: v.ExpiresAt, MaxAge: 30 * 24 * 3600})
		}
		if v, ok := out.(artworkRedirect); ok {
			w.Header().Set("Location", string(v))
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		write(w, 200, out)
	})
}
func (s *Server) auth(r *http.Request) (account, error) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		if c, err := r.Cookie("music_session"); err == nil {
			token = c.Value
		}
	}
	a := account{}
	if token == "" {
		return a, &apiError{401, "session_required"}
	}
	res := s.db.WithContext(r.Context()).Raw(`SELECT u.id,u.kind,s.expires_at FROM music_users u JOIN music_sessions s ON s.user_id=u.id WHERE s.hash=? AND s.expires_at>? AND u.app_id=?`, digest(token), s.opts.Now(), s.opts.AppID).Scan(&a)
	if res.Error != nil {
		return a, res.Error
	}
	if res.RowsAffected == 0 {
		return a, &apiError{401, "session_expired"}
	}
	return a, nil
}

type sessionInput struct {
	Transport    string `json:"transport"`
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
}

type artworkRedirect string
type artworkProvider interface {
	Artwork(context.Context, string) (string, error)
}

// Session response is produced by route; Handler attaches the cookie from its internal wrapper.
type sessionResult struct {
	User         account   `json:"user"`
	ExpiresAt    time.Time `json:"expiresAt"`
	SessionToken string    `json:"sessionToken,omitempty"`
	cookie       string
}

func (s *Server) issue(db *gorm.DB, a account, transport string) (*sessionResult, error) {
	if transport != "" && transport != "bearer" {
		return nil, invalid("invalid_transport")
	}
	token := secret()
	exp := s.opts.Now().Add(30 * 24 * time.Hour)
	if err := db.Exec("INSERT INTO music_sessions(hash,user_id,expires_at) VALUES(?,?,?)", digest(token), a.ID, exp).Error; err != nil {
		return nil, err
	}
	out := &sessionResult{User: a, ExpiresAt: exp, cookie: token}
	if transport == "bearer" {
		out.SessionToken = token
	}
	return out, nil
}
func (s *Server) session(r *http.Request, path string) (any, error) {
	var in sessionInput
	if err := body(r, &in); err != nil {
		return nil, err
	}
	if in.Transport != "" && in.Transport != "bearer" {
		return nil, invalid("invalid_transport")
	}
	db := s.db.WithContext(r.Context())
	if path == "auth/guest" {
		if a, err := s.auth(r); err == nil {
			return map[string]any{"user": a, "expiresAt": a.ExpiresAt}, nil
		}
		a := account{ID: secret(), Kind: "guest"}
		var out *sessionResult
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("INSERT INTO music_users(id,kind,app_id,tenant_key,subject) VALUES(?,?,?,?,?)", a.ID, a.Kind, s.opts.AppID, "guest", a.ID).Error; err != nil {
				return err
			}
			var err error
			out, err = s.issue(tx, a, in.Transport)
			return err
		})
		return out, err
	}
	if path == "auth/recover" {
		if len(in.RecoveryCode) < 40 {
			return nil, &apiError{401, "invalid_recovery_code"}
		}
		var a account
		res := db.Raw("SELECT id,kind FROM music_users WHERE recovery_hash=? AND app_id=?", digest(in.RecoveryCode), s.opts.AppID).Scan(&a)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, &apiError{401, "invalid_recovery_code"}
		}
		return s.issue(db, a, in.Transport)
	}
	if !s.authOptions.Feishu.Enabled {
		return nil, &apiError{503, "feishu_not_configured"}
	}
	ident, err := s.opts.Verifier.Verify(r.Context(), in.Code)
	if err != nil {
		return nil, &apiError{401, "feishu_code_rejected"}
	}
	if ident.AppID != s.opts.AppID || ident.TenantKey == "" || ident.Subject == "" {
		return nil, &apiError{401, "invalid_verified_identity"}
	}
	var a account
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO music_users(id,kind,app_id,tenant_key,subject) VALUES(?,'feishu',?,?,?) ON CONFLICT(app_id,tenant_key,subject) DO NOTHING`, secret(), ident.AppID, ident.TenantKey, ident.Subject).Error; err != nil {
			return err
		}
		return tx.Raw("SELECT id,kind FROM music_users WHERE app_id=? AND tenant_key=? AND subject=?", ident.AppID, ident.TenantKey, ident.Subject).Scan(&a).Error
	})
	if err != nil {
		return nil, err
	}
	return s.issue(db, a, in.Transport)
}
func (s *Server) route(r *http.Request) (any, error) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/music-api/v1/"), "/")
	if r.Method == "GET" && path == "auth/options" {
		return s.authOptions, nil
	}
	if r.Method == "GET" && strings.HasPrefix(path, "tracks/") && strings.HasSuffix(path, "/artwork") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "tracks/"), "/artwork")
		if p, ok := s.opts.Provider.(artworkProvider); ok {
			u, err := p.Artwork(r.Context(), id)
			return artworkRedirect(u), err
		}
		return nil, missing
	}
	if r.Method == "POST" && (path == "auth/guest" || path == "auth/recover" || path == "auth/feishu/exchange") {
		return s.session(r, path)
	}
	if strings.HasPrefix(path, "shared/") && r.Method == "GET" {
		return s.shared(r, strings.TrimPrefix(path, "shared/"))
	}
	a, err := s.auth(r)
	if err != nil {
		return nil, err
	}
	db := s.db.WithContext(r.Context())
	if path == "me" && r.Method == "GET" {
		return map[string]any{"user": a, "expiresAt": a.ExpiresAt}, nil
	}
	if path == "auth/logout" && r.Method == "POST" {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			if c, e := r.Cookie("music_session"); e == nil {
				token = c.Value
			}
		}
		return map[string]bool{"ok": true}, db.Exec("DELETE FROM music_sessions WHERE hash=?", digest(token)).Error
	}
	if path == "auth/recovery" && r.Method == "POST" {
		code := secret()
		err := db.Exec("UPDATE music_users SET recovery_hash=? WHERE id=?", digest(code), a.ID).Error
		return map[string]string{"recoveryCode": code}, err
	}
	if path == "search" && r.Method == "GET" {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" || len(q) > 200 {
			return nil, invalid("invalid_query")
		}
		if s.opts.Provider == nil {
			return nil, unavailable
		}
		tracks, err := s.opts.Provider.Search(r.Context(), q)
		if err != nil {
			return nil, unavailable
		}
		for _, t := range tracks {
			if err := saveTrack(db, t); err != nil {
				return nil, err
			}
		}
		return map[string]any{"tracks": tracks}, nil
	}
	if path == "tracks/register" && r.Method == "POST" {
		var in struct {
			Track Track `json:"track"`
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		// The descriptor supplies identity only. Shared metadata comes from the configured provider.
		return canonicalInputTrack(r.Context(), db, s.opts.Provider, in.Track)
	}
	if path == "tracks/details" && r.Method == "POST" {
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		if len(in.IDs) > 1000 {
			return nil, invalid("too_many_tracks")
		}
		out := []Track{}
		for _, id := range in.IDs {
			t, err := ensureTrack(r.Context(), db, s.opts.Provider, id)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return map[string]any{"tracks": out}, nil
	}
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] == "tracks" {
		id, err := url.PathUnescape(parts[1])
		if err != nil {
			return nil, invalid("invalid_id")
		}
		t, err := ensureTrack(r.Context(), db, s.opts.Provider, id)
		if err != nil {
			return nil, err
		}
		if len(parts) == 2 && r.Method == "GET" {
			return t, nil
		}
		if len(parts) == 3 && parts[2] == "resolve" && r.Method == "POST" {
			if s.opts.Provider == nil {
				return Resolution{TrackID: id, Status: "unavailable", Reason: "source_not_configured"}, nil
			}
			res, e := s.opts.Provider.Resolve(r.Context(), t)
			if e != nil {
				return nil, unavailable
			}
			return res, nil
		}
		if len(parts) == 3 && parts[2] == "lyrics" && r.Method == "GET" {
			if s.opts.Provider == nil {
				return Lyrics{TrackID: id}, nil
			}
			l, e := s.opts.Provider.Lyrics(r.Context(), t)
			if e != nil {
				return nil, unavailable
			}
			return l, nil
		}
	}
	if path == "playlists" || parts[0] == "playlists" {
		return s.playlists(r, a, path)
	}
	if parts[0] == "likes" {
		return s.likes(r, a, parts)
	}
	if path == "history" {
		return s.history(r, a)
	}
	if len(parts) == 2 && parts[0] == "queue" {
		return s.queue(r, a, parts[1])
	}
	if path == "export" && r.Method == "GET" {
		return s.export(db, a.ID)
	}
	if path == "import" && r.Method == "POST" {
		var in Export
		if err := body(r, &in); err != nil {
			return nil, err
		}
		return s.importData(db, a.ID, in)
	}
	if parts[0] == "shares" {
		return s.shares(r, a, parts)
	}
	if len(parts) == 3 && parts[0] == "shared" && parts[2] == "copy" && r.Method == "POST" {
		e, err := s.shared(r, parts[1])
		if err != nil {
			return nil, err
		}
		return s.importData(db, a.ID, e)
	}
	return nil, missing
}

// HTTP wraps Handler with a session-cookie adapter without ever exposing cookie tokens to JS.
func (s *Server) HTTP() http.Handler {
	// Reuse the same middleware with response interception only for issued sessions.
	return s.Handler()
}
