// Package webui 实现 BetaGo 管理后台的只读/读写 REST API。
//
// 设计目标：
//   - 前后端分离：本包只提供 JSON API，前端由独立的 Vue 工程消费（见仓库 webui/ 目录）；
//   - 与机器人主流程解耦：以独立 runtime.Module + 独立监听端口运行，崩溃不影响消息处理；
//   - 复用既有领域能力：群列表/头像走 chatmetrics 与 larkchat，功能开关与配置走
//     application/config.Manager，token 消耗走 llm_token_usage_records 表。
//
// 不依赖额外 Web 框架，沿用 internal/runtime/health_http.go 的 net/http + sonic 风格。
package webui

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

// Options 聚合 WebUI 模块运行所需的全部依赖与配置。
//
// 依赖通过显式注入传入（DBProvider、ConfigManager），而不是在包内引用全局单例，
// 便于测试时用 httptest + 内存依赖替身覆盖。DBProvider 采用惰性求值，因为底层
// *gorm.DB 在 db 模块 Init 阶段才完成初始化，晚于本模块的构造时机。
type Options struct {
	Config        *infraConfig.WebUIConfig
	ConfigManager ConfigManager
	DBProvider    func() *gorm.DB
	ChatService   ChatService
	MemberCount   MemberCountFunc
	MemberList    MemberListFunc
	MessageStats  MessageStatsFunc
	RecentChatIDs RecentChatIDsFunc
	Now           func() time.Time
	// RobotName 用于在多 bot 场景下前端区分不同实例；空串时回退为 "unknown"。
	RobotName string
	// Instance 是部署实例名或 Lark AppID，便于运维定位。
	Instance string
}

// Module 承载 WebUI 的 HTTP 服务生命周期，实现 runtime.Module 契约。
type Module struct {
	opts            Options
	addr            string
	shutdownTimeout time.Duration
	server          *http.Server
	listener        net.Listener
	srv             *Server
}

// NewModule 根据注入的依赖构造一个 WebUI 模块。
func NewModule(opts Options) *Module {
	addr := ""
	shutdown := 10 * time.Second
	if opts.Config != nil {
		addr = strings.TrimSpace(opts.Config.Addr)
		if opts.Config.ShutdownTimeoutSeconds > 0 {
			shutdown = time.Duration(opts.Config.ShutdownTimeoutSeconds) * time.Second
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Module{
		opts:            opts,
		addr:            addr,
		shutdownTimeout: shutdown,
	}
}

// Name 返回模块在健康注册表中的稳定名称。
func (m *Module) Name() string { return "webui_http" }

// Critical 返回 false：WebUI 是运维辅助面，不应阻断核心机器人启动。
func (m *Module) Critical() bool { return false }

// Init 校验模块是否启用。空地址表示运维显式关闭，按 ErrDisabled 处理。
func (m *Module) Init(context.Context) error {
	if m == nil {
		return errors.New("webui module is nil")
	}
	if m.addr == "" {
		return appruntime.ErrDisabled
	}
	var db *gorm.DB
	if m.opts.DBProvider != nil {
		db = m.opts.DBProvider()
	}
	m.srv = NewServer(m.opts, db)
	return nil
}

// Start 绑定监听端口并启动 HTTP 服务。
func (m *Module) Start(context.Context) error {
	listener, err := net.Listen("tcp", m.addr)
	if err != nil {
		return err
	}
	m.listener = listener
	m.server = &http.Server{
		Handler:           m.srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go func() {
		_ = m.server.Serve(listener)
	}()
	return nil
}

// Ready 判断 HTTP 服务是否已成功绑定监听端口。
func (m *Module) Ready(context.Context) error {
	if m == nil || m.server == nil || m.listener == nil {
		return errors.New("webui http server not started")
	}
	return nil
}

// Stop 优雅关闭 HTTP 服务。
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, m.shutdownTimeout)
	defer cancel()
	return m.server.Shutdown(shutdownCtx)
}

// Stats 暴露监听地址，便于状态面排查。
func (m *Module) Stats() map[string]any {
	if m == nil {
		return nil
	}
	return map[string]any{"addr": m.addr}
}

// writeJSON 统一 API 的 JSON 输出。
func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = sonic.ConfigDefault.NewEncoder(w).Encode(payload)
}

// errorResponse 是统一的错误返回结构。
type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, statusCode int, msg string) {
	writeJSON(w, statusCode, errorResponse{Error: msg})
}
