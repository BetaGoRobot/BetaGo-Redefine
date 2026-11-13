package music

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"gorm.io/gorm"
)

type Module struct {
	opts     Options
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	serveErr error
}

var _ appruntime.Module = (*Module)(nil)

func OptionsFromEnv(provider func() *gorm.DB) Options {
	o := Options{SkipMigration: os.Getenv("MUSIC_MIGRATE") == "false", DBProvider: provider, Addr: os.Getenv("MUSIC_ADDR"), Origin: os.Getenv("MUSIC_ORIGIN"), AppID: os.Getenv("MUSIC_APP_ID"), StaticDir: os.Getenv("MUSIC_STATIC_DIR"), SecureCookie: os.Getenv("MUSIC_COOKIE_SECURE") != "false", Provider: AnonymousNetease{BaseURL: os.Getenv("MUSIC_NETEASE_URL"), OwnedMediaHost: os.Getenv("MUSIC_OWNED_MEDIA_HOST"), ServerCookie: os.Getenv("MUSIC_NETEASE_COOKIE")}}
	cacheEnabled := os.Getenv("MUSIC_MINIO_CACHE_ENABLED") == "true"
	cacheConfig := ownedCacheConfig{endpoint: os.Getenv("MUSIC_MINIO_ENDPOINT"), publicOrigin: os.Getenv("MUSIC_MINIO_PUBLIC_ORIGIN"), bucket: os.Getenv("MUSIC_MINIO_BUCKET"), region: os.Getenv("MUSIC_MINIO_REGION"), accessKey: os.Getenv("MUSIC_MINIO_ACCESS_KEY"), secretKey: os.Getenv("MUSIC_MINIO_SECRET_KEY"), writeEnabled: os.Getenv("MUSIC_MINIO_CACHE_WRITE_ENABLED") == "true", writers: make(chan struct{}, 2)}
	if cacheEnabled {
		p, err := newOwnedCacheProvider(cacheConfig, o.Provider)
		if err != nil {
			o.providerErr, o.Provider = err, failedCacheProvider{}
		} else {
			o.Provider = p
		}
	}
	router := SourceRouter{Primary: o.Provider, Sources: map[string]Provider{}}
	if endpoint := strings.TrimSpace(os.Getenv("MUSIC_KUWO_API_URL")); endpoint != "" {
		var alternative Provider = KuwoProvider{BaseURL: endpoint}
		if cacheEnabled {
			config := cacheConfig
			config.source = "kuwo"
			cached, err := newOwnedCacheProvider(config, alternative)
			if err != nil {
				o.providerErr, alternative = err, failedCacheProvider{}
			} else {
				alternative = cached
			}
		}
		router.Sources["kuwo"] = alternative
	}
	if len(router.Sources) > 0 {
		o.Provider = router
	}
	if secret := os.Getenv("MUSIC_FEISHU_APP_SECRET"); secret != "" {
		o.Verifier = FeishuVerifier{AppID: o.AppID, AppSecret: secret, RedirectURI: os.Getenv("MUSIC_FEISHU_REDIRECT_URI")}
	}
	return o
}
func NewModule(opts Options) *Module { return &Module{opts: opts} }
func (*Module) Name() string         { return "music_http" }
func (*Module) Critical() bool       { return false }
func (m *Module) Init(ctx context.Context) error {
	if m.opts.Addr == "" {
		return appruntime.ErrDisabled
	}
	if m.opts.providerErr != nil {
		return m.opts.providerErr
	}
	u, err := url.Parse(m.opts.Origin)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" {
		return errors.New("MUSIC_ORIGIN must be an exact http(s) origin")
	}
	if m.opts.AppID == "" {
		return errors.New("MUSIC_APP_ID required for application isolation")
	}
	if m.opts.DBProvider == nil || m.opts.DBProvider() == nil {
		return errors.New("music database unavailable")
	}
	db := m.opts.DBProvider()
	if m.opts.SkipMigration {
		for _, table := range []string{"music_users", "music_sessions", "music_tracks", "music_playlists", "music_entries", "music_likes", "music_history", "music_queues", "music_shares"} {
			var exists bool
			if err := db.WithContext(ctx).Raw("SELECT to_regclass(?) IS NOT NULL", table).Scan(&exists).Error; err != nil {
				return err
			}
			if !exists {
				return errors.New("apply music migration before starting with MUSIC_MIGRATE=false")
			}
		}
		var canonicalColumn bool
		if err := db.WithContext(ctx).Raw("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='music_tracks' AND column_name='canonical_verified')").Scan(&canonicalColumn).Error; err != nil {
			return err
		}
		if !canonicalColumn {
			return errors.New("apply music canonical metadata migration before starting with MUSIC_MIGRATE=false")
		}
	} else if err := Migrate(db.WithContext(ctx)); err != nil {
		return err
	}
	s := NewServer(db, m.opts)
	mux := http.NewServeMux()
	mux.Handle("/music-api/v1/", s.HTTP())
	if m.opts.StaticDir != "" {
		root, err := filepath.Abs(m.opts.StaticDir)
		if err != nil {
			return err
		}
		files := http.FileServer(http.Dir(root))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			path := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
			st, err := os.Stat(path)
			if err == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
			if strings.Contains(filepath.Base(r.URL.Path), ".") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
		})
	}
	m.server = &http.Server{Addr: m.opts.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	return nil
}
func (m *Module) Start(context.Context) error {
	l, err := net.Listen("tcp", m.opts.Addr)
	if err != nil {
		return err
	}
	m.listener = l
	go func() {
		err := m.server.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.mu.Lock()
			m.serveErr = err
			m.mu.Unlock()
		}
	}()
	return nil
}
func (m *Module) Ready(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.serveErr != nil {
		return m.serveErr
	}
	if m.listener == nil {
		return errors.New("music listener unavailable")
	}
	return nil
}
func (m *Module) Stop(ctx context.Context) error {
	if m.server == nil {
		return nil
	}
	return m.server.Shutdown(ctx)
}
