package akshareapi

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

type backend interface {
	client() *Client
	disableReason() string
}

type noopBackend struct {
	reason string
}

func (b noopBackend) client() *Client {
	return nil
}

func (b noopBackend) disableReason() string {
	return b.reason
}

type liveBackend struct {
	api *Client
}

func (b liveBackend) client() *Client {
	return b.api
}

func (b liveBackend) disableReason() string {
	return ""
}

var (
	defaultBackend backend = noopBackend{reason: "akshareapi not initialized"}
	warnOnce       sync.Once
	errUnavailable = errors.New("akshareapi unavailable")
)

func Init() {
	cfg := config.Get().AKToolConfig
	if cfg == nil || cfg.BaseURL == "" {
		setNoop("akshareapi config missing or base url empty")
		return
	}
	defaultBackend = liveBackend{api: NewClient(cfg.BaseURL, nil)}
}

func Status() (bool, string) {
	reason := defaultBackend.disableReason()
	return reason == "", reason
}

func ErrUnavailable() error {
	reason := defaultBackend.disableReason()
	if reason == "" {
		return errUnavailable
	}
	return fmt.Errorf("%w: %s", errUnavailable, reason)
}

func IsUnavailable(err error) bool {
	return errors.Is(err, errUnavailable)
}

func CallRows(ctx context.Context, endpoint Endpoint, params any) (Rows, error) {
	client, err := runtimeClient()
	if err != nil {
		return nil, err
	}
	return client.CallRows(ctx, endpoint, params)
}

func CallByName(ctx context.Context, endpointName string, params any) (Rows, error) {
	client, err := runtimeClient()
	if err != nil {
		return nil, err
	}
	return client.CallByName(ctx, endpointName, params)
}

func CallInto(ctx context.Context, endpoint Endpoint, params any, out any) error {
	client, err := runtimeClient()
	if err != nil {
		return err
	}
	return client.CallInto(ctx, endpoint, params, out)
}

func setNoop(reason string) {
	defaultBackend = noopBackend{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("AKShare API disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

func runtimeClient() (*Client, error) {
	client := defaultBackend.client()
	if client == nil {
		return nil, ErrUnavailable()
	}
	return client, nil
}
