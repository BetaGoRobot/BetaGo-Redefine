package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// LarkWSModule 把上游 websocket client 适配进统一运行时生命周期。
type LarkWSModule struct {
	appID      string
	appSecret  string
	dispatcher *dispatcher.EventDispatcher
	client     *larkws.Client
	startErrCh chan error
}

// NewLarkWSModule 把飞书/Lark 的 websocket client 包装成运行时模块，让
// ingress 和其他 critical 模块一样接受统一管理。
func NewLarkWSModule(appID, appSecret string, eventDispatcher *dispatcher.EventDispatcher) *LarkWSModule {
	return &LarkWSModule{
		appID:      appID,
		appSecret:  appSecret,
		dispatcher: eventDispatcher,
		startErrCh: make(chan error, 1),
	}
}

// Name 返回 websocket ingress 在注册表中的名字。
func (m *LarkWSModule) Name() string {
	return "lark_ws"
}

// Critical 返回 true，因为这个进程的核心职责就是消费 Lark 事件；如果
// websocket ingress 起不来，整个服务就没有意义。
func (m *LarkWSModule) Critical() bool {
	return true
}

// Init 校验凭证并构造底层 websocket client。
func (m *LarkWSModule) Init(ctx context.Context) (err error) {
	ctx, span := otel.StartNamed(ctx, "runtime.lark_ws.init")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("module.name", m.Name()),
		attribute.Bool("module.critical", m.Critical()),
	)

	if m == nil {
		return errors.New("lark ws module is nil")
	}
	if m.appID == "" || m.appSecret == "" {
		return errors.New("lark ws app credentials are required")
	}
	if m.dispatcher == nil {
		return errors.New("lark ws dispatcher is required")
	}
	m.client = larkws.NewClient(
		m.appID,
		m.appSecret,
		larkws.WithEventHandler(m.dispatcher),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)
	return nil
}

// Start 在后台拉起上游 websocket client，并等待一个很短的时间窗口来捕获
// 即时启动错误。
//
// 上游库会在 Client.Start 内部持续阻塞直到连接结束，所以这里拿不到
// “已经完全连上”的明确回调。500ms 的等待窗口本质上是一个 fail-fast
// 探针：它能抓住明显的启动失败，同时不会无限阻塞整个启动流程。
func (m *LarkWSModule) Start(ctx context.Context) (err error) {
	if m == nil || m.client == nil {
		return errors.New("lark ws client not initialized")
	}

	ctx, span := otel.StartNamed(ctx, "runtime.lark_ws.start")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("module.name", m.Name()),
		attribute.Int("startup_probe_ms", 500),
	)

	go func() {
		if startErr := m.client.Start(ctx); startErr != nil {
			select {
			case m.startErrCh <- startErr:
			default:
			}
		}
	}()

	select {
	case err = <-m.startErrCh:
		span.AddEvent("lark_ws.start.result", trace.WithAttributes(attribute.String("result", "error")))
		return err
	case <-time.After(500 * time.Millisecond):
		span.AddEvent("lark_ws.start.result", trace.WithAttributes(attribute.String("result", "accepted")))
		return nil
	case <-ctx.Done():
		err = ctx.Err()
		return err
	}
}

// Ready 用来上报 Start 返回之后异步观察到的启动错误。
func (m *LarkWSModule) Ready(ctx context.Context) (err error) {
	ctx, span := otel.StartNamed(ctx, "runtime.lark_ws.ready")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(attribute.String("module.name", m.Name()))

	select {
	case err = <-m.startErrCh:
		return err
	default:
		return nil
	}
}

// Stop 只负责记录当前上游限制。运行时仍然调用它，是为了把这一限制集中
// 收敛在一个位置，并通过 Stats() 暴露出去。
func (m *LarkWSModule) Stop(ctx context.Context) (err error) {
	ctx, span := otel.StartNamed(ctx, "runtime.lark_ws.stop")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("module.name", m.Name()),
		attribute.Bool("graceful_stop_supported", false),
	)
	span.AddEvent("graceful_stop_unsupported")
	return nil
}

// Stats 用来把“无法优雅关闭”这个已知限制暴露给管理面。
func (m *LarkWSModule) Stats() map[string]any {
	return map[string]any{
		"graceful_stop_supported": false,
		"library":                 "github.com/larksuite/oapi-sdk-go/v3/ws",
	}
}
