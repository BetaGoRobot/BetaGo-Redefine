package webui

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"gorm.io/gorm"
)

// Server 持有 WebUI 的依赖并构建 HTTP 路由。
type Server struct {
	cfg              ConfigManager
	chats            ChatService
	memberCount      MemberCountFunc
	memberList       MemberListFunc
	messageStats     MessageStatsFunc
	recentChatIDs    RecentChatIDsFunc
	chatActivity     ChatActivityFunc
	chatKeywords     ChatKeywordsFunc
	chatCommands     ChatCommandsFunc
	chatTopSenders   ChatTopSendersFunc
	chatMessageKinds ChatMessageKindsFunc
	chatCommandTrend ChatCommandTrendFunc
	chatTopMentions  ChatTopMentionsFunc
	chatTopicTrend   ChatTopicTrendFunc
	now              func() time.Time

	authToken   string
	corsOrigins []string
	store       *tokenStatsStore

	robotName   string
	instance    string
	botID       string
	appID       string
	botOpenID   string
	evaluations EvaluationWorkbench
	rollouts    AgenticRolloutService
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
	botID := strings.TrimSpace(opts.BotID)
	if botID == "" {
		// 兜底：用 Instance（Lark AppID）拼出与 llmusage SetDefaultBotIDProvider 一致的 bot 标识。
		if inst := strings.TrimSpace(opts.Instance); inst != "" {
			botID = "lark:" + inst
		}
	}
	evaluations := opts.EvaluationWorkbench
	if evaluations == nil && db != nil {
		evaluations = newEvaluationWorkbenchStore(db, opts.AppID, opts.BotOpenID)
	}
	return &Server{
		cfg:              opts.ConfigManager,
		chats:            opts.ChatService,
		memberCount:      opts.MemberCount,
		memberList:       opts.MemberList,
		messageStats:     opts.MessageStats,
		recentChatIDs:    opts.RecentChatIDs,
		chatActivity:     opts.ChatActivity,
		chatKeywords:     opts.ChatKeywords,
		chatCommands:     opts.ChatCommands,
		chatTopSenders:   opts.ChatTopSenders,
		chatMessageKinds: opts.ChatMessageKinds,
		chatCommandTrend: opts.ChatCommandTrend,
		chatTopMentions:  opts.ChatTopMentions,
		chatTopicTrend:   opts.ChatTopicTrend,
		now:              now,
		authToken:        authToken,
		corsOrigins:      corsOrigins,
		store:            newTokenStatsStore(db, botID),
		robotName:        strings.TrimSpace(opts.RobotName),
		instance:         strings.TrimSpace(opts.Instance),
		botID:            botID,
		appID:            strings.TrimSpace(opts.AppID),
		botOpenID:        strings.TrimSpace(opts.BotOpenID),
		evaluations:      evaluations,
		rollouts:         opts.AgenticRollouts,
	}
}

// Handler 构建完整的 HTTP 处理器，含 CORS 与鉴权中间件。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Go 1.22+ 的方法+通配路由模式，避免手写路径解析。
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/chats", s.handleListChats)
	mux.HandleFunc("GET /api/chats/{chatID}", s.handleGetChat)
	mux.HandleFunc("GET /api/chats/{chatID}/members", s.handleListMembers)
	mux.HandleFunc("GET /api/chats/{chatID}/stats", s.handleStats)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/activity", s.handleInsightsActivity)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/keywords", s.handleInsightsKeywords)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/commands", s.handleInsightsCommands)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/top_senders", s.handleInsightsTopSenders)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/message_kinds", s.handleInsightsMessageKinds)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/command_trend", s.handleInsightsCommandTrend)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/top_mentions", s.handleInsightsTopMentions)
	mux.HandleFunc("GET /api/chats/{chatID}/insights/topic_trend", s.handleInsightsTopicTrend)
	mux.HandleFunc("GET /api/chats/{chatID}/features", s.handleListFeatures)
	mux.HandleFunc("PUT /api/chats/{chatID}/features/{name}", s.handleSetFeature)
	mux.HandleFunc("GET /api/chats/{chatID}/configs", s.handleListConfigs)
	mux.HandleFunc("PUT /api/chats/{chatID}/configs/{key}", s.handleSetConfig)
	mux.HandleFunc("DELETE /api/chats/{chatID}/configs/{key}", s.handleDeleteConfig)
	mux.HandleFunc("GET /api/chats/{chatID}/agentic-rollout", s.handleGetAgenticRollout)
	mux.HandleFunc("PUT /api/chats/{chatID}/agentic-rollout", s.handlePutAgenticRollout)
	mux.HandleFunc("GET /api/agentic-rollouts", s.handleListAgenticRollouts)
	mux.HandleFunc("POST /api/agentic-rollouts/batch", s.handleBatchAgenticRollouts)
	mux.HandleFunc("GET /api/evaluations", s.handleListEvaluations)
	mux.HandleFunc("GET /api/evaluations/{episodeID}", s.handleGetEvaluation)
	mux.HandleFunc("POST /api/evaluations/{episodeID}/judgments", s.handleAppendEvaluationJudgment)

	return s.withMetrics(s.withCORS(s.withAuth(mux)))
}

// withAuth 对写操作和敏感读取强制 Bearer Token 鉴权；未配置 token 时保留
// 历史放行行为。评测接口会在自己的 handler 中额外要求 token 必须已配置。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" || !requiresWebUIAuth(r.Method, r.URL.Path) {
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

// requiresWebUIAuth 是 WebUI 的服务端纵深鉴权边界。公开统计读取保持匿名，
// 身份、评测和管理配置读取与所有非 GET 写请求必须鉴权。
func requiresWebUIAuth(method, path string) bool {
	if method == http.MethodOptions {
		return false
	}
	if method != http.MethodGet {
		return true
	}

	path = strings.TrimSpace(path)
	if path == "/api/evaluations" ||
		strings.HasPrefix(path, "/api/evaluations/") ||
		path == "/api/agentic-rollouts" ||
		strings.HasPrefix(path, "/api/agentic-rollouts/") {
		return true
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "chats" {
		return false
	}
	switch parts[3] {
	case "members", "configs", "features", "agentic-rollout":
		return true
	case "insights":
		return len(parts) >= 5 &&
			(parts[4] == "top_senders" || parts[4] == "top_mentions")
	default:
		return false
	}
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, PATCH, DELETE, OPTIONS")
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

func (s *Server) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		route := normalizeMetricsRoute(r.Pattern)
		method := normalizeMetricsLabel(r.Method)

		metrics.GetOrCreateGauge(fmt.Sprintf(
			`betago_webui_http_inflight_requests{method=%q,route=%q}`,
			method, route,
		), nil).Inc()
		defer metrics.GetOrCreateGauge(fmt.Sprintf(
			`betago_webui_http_inflight_requests{method=%q,route=%q}`,
			method, route,
		), nil).Dec()

		next.ServeHTTP(rec, r)

		status := strconv.Itoa(rec.status)
		metrics.GetOrCreateCounter(fmt.Sprintf(
			`betago_webui_http_requests_total{method=%q,route=%q,status=%q}`,
			method, route, status,
		)).Inc()
		if rec.status >= 500 {
			metrics.GetOrCreateCounter(fmt.Sprintf(
				`betago_webui_http_errors_total{method=%q,route=%q,status=%q}`,
				method, route, status,
			)).Inc()
		}
		metrics.GetOrCreateHistogram(fmt.Sprintf(
			`betago_webui_http_request_duration_seconds{method=%q,route=%q,status=%q}`,
			method, route, status,
		)).UpdateDuration(start)
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
	robot := s.robotName
	if robot == "" {
		robot = "unknown"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"auth":       s.authToken != "",
		"timestamp":  s.now().Unix(),
		"robot_name": robot,
		"instance":   s.instance,
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func normalizeMetricsRoute(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "unknown"
	}
	if method, path, ok := strings.Cut(pattern, " "); ok {
		_ = method
		pattern = path
	}
	return normalizeMetricsLabel(pattern)
}

func normalizeMetricsLabel(v string) string {
	v = strings.TrimSpace(strings.ToValidUTF8(v, "?"))
	if v == "" {
		return "unknown"
	}
	const maxLen = 80
	runes := []rune(v)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return v
}

// isTruthy 解析常见的“开启”取值（1/true/yes/on），用于可选 query 开关。
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// webuiCacheKey 生成带窗口维度的服务端缓存键。
func webuiCacheKey(name string, windowDays int) string {
	return "webui:" + name + ":w" + strconv.Itoa(windowDays)
}

// round2 保留两位小数，用于派生均值。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
