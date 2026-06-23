package webui

import (
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Server 持有 WebUI 的依赖并构建 HTTP 路由。
type Server struct {
	cfg          ConfigManager
	chats        ChatService
	messageStats MessageStatsFunc
	now          func() time.Time

	authToken    string
	corsOrigins  []string
	store        *tokenStatsStore
}

// NewServer 根据注入的依赖构造 Server。db 由模块在 Init 阶段惰性解析后传入。
func NewServer(opts Options, db *gorm.DB) *Server {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	var authToken string
	var corsOrigins []string
	if opts.Config != nil {
		authToken = strings.TrimSpace(opts.Config.AuthToken)
		corsOrigins = normalizeOrigins(opts.Config.CORSAllowOrigins)
	}
	return &Server{
		cfg:          opts.ConfigManager,
		chats:        opts.ChatService,
		messageStats: opts.MessageStats,
		now:          now,
		authToken:    authToken,
		corsOrigins:  corsOrigins,
		store:        newTokenStatsStore(db),
	}
}

// Handler 构建完整的 HTTP 处理器，含 CORS 与鉴权中间件。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Go 1.22+ 的方法+通配路由模式，避免手写路径解析。
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/chats", s.handleListChats)
	mux.HandleFunc("GET /api/chats/{chatID}", s.handleGetChat)
	mux.HandleFunc("GET /api/chats/{chatID}/stats", s.handleStats)
	mux.HandleFunc("GET /api/chats/{chatID}/features", s.handleListFeatures)
	mux.HandleFunc("PUT /api/chats/{chatID}/features/{name}", s.handleSetFeature)
	mux.HandleFunc("GET /api/chats/{chatID}/configs", s.handleListConfigs)
	mux.HandleFunc("PUT /api/chats/{chatID}/configs/{key}", s.handleSetConfig)
	mux.HandleFunc("DELETE /api/chats/{chatID}/configs/{key}", s.handleDeleteConfig)

	return s.withCORS(s.withAuth(mux))
}

// withAuth 对写操作强制 Bearer Token 鉴权；未配置 token 时全部放行。
// GET 与 CORS 预检（OPTIONS）始终放行，便于前端只读浏览。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" || r.Method == http.MethodGet || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !s.checkBearer(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized: missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) checkBearer(r *http.Request) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token != "" && token == s.authToken
}

// withCORS 处理跨域，支持前后端分离部署。
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed := s.resolveAllowedOrigin(origin); allowed != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowed)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolveAllowedOrigin 根据配置返回应回写的 Allow-Origin 值。
// 未配置允许列表时回退为 "*"（仅建议内网使用）。
func (s *Server) resolveAllowedOrigin(origin string) string {
	if len(s.corsOrigins) == 0 {
		return "*"
	}
	if origin == "" {
		return ""
	}
	for _, o := range s.corsOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return origin
		}
	}
	return ""
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"auth":      s.authToken != "",
		"timestamp": s.now().Unix(),
	})
}

func normalizeOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}
