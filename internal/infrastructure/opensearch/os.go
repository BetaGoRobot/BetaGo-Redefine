package opensearch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/opensearch-project/opensearch-go/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var opensearchWarnOnce sync.Once
var backend searchBackend = noopBackend{reason: "opensearch not initialized"}

var opensearchDomain = os.Getenv("OPENSEARCH_DOMAIN")

type searchBackend interface {
	Reason() string
	InsertData(ctx context.Context, index string, id string, data any) error
	SearchData(ctx context.Context, index string, data any) (*opensearchapi.SearchResp, error)
	SearchDataStr(ctx context.Context, index string, data string) (*opensearchapi.SearchResp, error)
}

type noopBackend struct {
	reason string
}

func (n noopBackend) Reason() string {
	return n.reason
}

func (n noopBackend) InsertData(context.Context, string, string, any) error {
	return nil
}

func (n noopBackend) SearchData(context.Context, string, any) (*opensearchapi.SearchResp, error) {
	return nil, errors.New(n.reason)
}

func (n noopBackend) SearchDataStr(context.Context, string, string) (*opensearchapi.SearchResp, error) {
	return nil, errors.New(n.reason)
}

type liveBackend struct {
	client *opensearchapi.Client
}

func (l liveBackend) Reason() string {
	return ""
}

func (l liveBackend) InsertData(ctx context.Context, index string, id string, data any) (err error) {
	ctx, span := otel.Start(ctx,
		trace.WithAttributes(
			attribute.String("index.name", index),
			attribute.String("document.id", id),
			attribute.String("payload.type", fmt.Sprintf("%T", data)),
		),
	)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	index += "-" + time.Now().In(utils.UTC8Loc()).Format("2006-01-02")
	req := opensearchapi.IndexReq{
		Index:      index,
		DocumentID: id,
		Body:       opensearchutil.NewJSONReader(data),
	}
	resp, err := l.client.Index(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("Index error", zap.Error(err), zap.Any("resp", resp))
		return err
	}
	return nil
}

func (l liveBackend) SearchData(ctx context.Context, index string, data any) (resp *opensearchapi.SearchResp, err error) {
	ctx, span := otel.Start(ctx,
		trace.WithAttributes(
			attribute.String("index.name", index),
			attribute.String("payload.type", fmt.Sprintf("%T", data)),
		),
	)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := &opensearchapi.SearchReq{
		Indices: []string{index},
		Body:    opensearchutil.NewJSONReader(data),
	}
	return l.client.Search(ctx, req)
}

func (l liveBackend) SearchDataStr(ctx context.Context, index string, data string) (resp *opensearchapi.SearchResp, err error) {
	ctx, span := otel.Start(ctx,
		trace.WithAttributes(attribute.String("index.name", index)),
	)
	span.SetAttributes(otel.PreviewAttrs("query", data, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := &opensearchapi.SearchReq{
		Indices: []string{index},
		Body:    strings.NewReader(data),
	}
	return l.client.Search(ctx, req)
}

func Init(conf *config.OpensearchConfig) {
	if conf == nil || conf.Domain == "" {
		setNoop("opensearch config missing or incomplete")
		return
	}
	opensearchDomain = conf.Domain
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Addresses: []string{
				"https://" + opensearchDomain + ":9200",
				"https://" + opensearchDomain + ":9200",
			},
			Username: conf.User,
			Password: conf.Password,
		},
	})
	if err != nil {
		setNoop("opensearch client init failed: " + err.Error())
		return
	}
	backend = liveBackend{client: client}
}

func setNoop(reason string) {
	backend = noopBackend{reason: reason}
	opensearchWarnOnce.Do(func() {
		logs.L().Warn("OpenSearch disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

func unavailableErr() error {
	return errors.New(backend.Reason())
}

func Status() (bool, string) {
	reason := backend.Reason()
	return reason == "", reason
}

func InsertData(ctx context.Context, index string, id string, data any) (err error) {
	return backend.InsertData(ctx, index, id, data)
}

func SearchData(ctx context.Context, index string, data any) (resp *opensearchapi.SearchResp, err error) {
	return backend.SearchData(ctx, index, data)
}

func SearchDataStr(ctx context.Context, index string, data string) (resp *opensearchapi.SearchResp, err error) {
	return backend.SearchDataStr(ctx, index, data)
}
