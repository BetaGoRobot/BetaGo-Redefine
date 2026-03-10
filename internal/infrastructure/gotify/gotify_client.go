package gotify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/go-openapi/runtime"
	"github.com/gotify/go-api-client/v2/auth"
	"github.com/gotify/go-api-client/v2/client"
	"github.com/gotify/go-api-client/v2/client/message"
	"github.com/gotify/go-api-client/v2/gotify"
	"github.com/gotify/go-api-client/v2/models"
	"go.uber.org/zap"
)

var (
	defaultSender Sender = noopSender{reason: "gotify not initialized"}
	warnOnce      sync.Once
)

type Sender interface {
	SendMessage(ctx context.Context, title, msg string, priority int)
}

type noopSender struct {
	reason string
}

func (n noopSender) SendMessage(context.Context, string, string, int) {}

type clientSender struct {
	token  runtime.ClientAuthInfoWriter
	client *client.GotifyREST
}

func Init() {
	config := config.Get().GotifyConfig
	if config == nil || config.URL == "" || config.ApplicationToken == "" {
		setNoop("gotify config missing or incomplete")
		return
	}
	gotifyURLParsed, err := url.Parse(config.URL)
	if err != nil {
		setNoop("error parsing gotify url: " + err.Error())
		return
	}
	defaultSender = clientSender{
		token:  auth.TokenAuth(config.ApplicationToken),
		client: gotify.NewClient(gotifyURLParsed, &http.Client{}),
	}
}

func setNoop(reason string) {
	defaultSender = noopSender{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("Gotify disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

func ErrUnavailable() error {
	if sender, ok := defaultSender.(noopSender); ok {
		return errors.New(sender.reason)
	}
	return nil
}

func SendMessage(ctx context.Context, title, msg string, priority int) {
	defaultSender.SendMessage(ctx, title, msg, priority)
}

func (s clientSender) SendMessage(ctx context.Context, title, msg string, priority int) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	logs.L().Ctx(ctx).Info("SendMessage...")

	if title == "" {
		title = "BetaGo Notification"
	}
	title = "[" + config.Get().BaseInfo.RobotName + "]" + title
	params := message.NewCreateMessageParams()
	params.Body = &models.MessageExternal{
		Title:    title,
		Message:  msg,
		Priority: priority,
		Extras: map[string]interface{}{
			"client::display": map[string]string{"contentType": "text/markdown"},
		},
	}

	_, err := s.client.Message.CreateMessage(params, s.token)
	if err != nil {
		logs.L().Ctx(ctx).Error("Could not send message", zap.Error(err))
		return
	}
	logs.L().Ctx(ctx).Info("Gotify Message Sent!")
}
