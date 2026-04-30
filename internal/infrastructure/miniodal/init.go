package miniodal

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

type backend interface {
	Reason() string
	Client(ClientType) *minio.Client
	ExpireTime() time.Duration
}

type noopBackend struct {
	reason string
}

func (b noopBackend) Reason() string {
	return b.reason
}

func (b noopBackend) Client(ClientType) *minio.Client {
	return nil
}

func (b noopBackend) ExpireTime() time.Duration {
	return 0
}

type liveBackend struct {
	internal *minio.Client
	external *minio.Client
	expire   time.Duration
}

func (b liveBackend) Reason() string {
	return ""
}

func (b liveBackend) Client(clientType ClientType) *minio.Client {
	if clientType == Internal {
		return b.internal
	}
	return b.external
}

func (b liveBackend) ExpireTime() time.Duration {
	return b.expire
}

var (
	defaultBackend backend = noopBackend{reason: "minio not initialized"}
	warnOnce       sync.Once
)

func Init(conf *config.MinioConfig) {
	if conf == nil || conf.Internal == nil || conf.External == nil || conf.AK == "" || conf.SK == "" || conf.ExpireTime == "" {
		setNoop("minio config missing or incomplete")
		return
	}
	internalClient, err := minio.New(conf.Internal.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(conf.AK, conf.SK, ""),
		Secure: conf.Internal.UseSSL,
	})
	if err != nil {
		setNoop("internal minio client init failed: " + err.Error())
		return
	}

	externalClient, err := minio.New(conf.External.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(conf.AK, conf.SK, ""),
		Secure: conf.External.UseSSL,
	})
	if err != nil {
		setNoop("external minio client init failed: " + err.Error())
		return
	}

	expireTime, err := time.ParseDuration(conf.ExpireTime)
	if err != nil {
		setNoop("invalid expire time format: " + err.Error())
		return
	}

	defaultBackend = liveBackend{
		internal: internalClient,
		external: externalClient,
		expire:   expireTime,
	}
	logs.L().Info("MinIO clients initialized successfully")
}

func ErrUnavailable() error {
	reason := defaultBackend.Reason()
	if reason == "" {
		reason = "minio not initialized"
	}
	return errors.New(reason)
}

func Status() (bool, string) {
	reason := defaultBackend.Reason()
	return reason == "", reason
}

func setNoop(reason string) {
	defaultBackend = noopBackend{reason: reason}
	warnOnce.Do(func() {
		logs.L().Warn("MinIO disabled, falling back to noop",
			zap.String("reason", reason),
		)
	})
}

func internalCli() *minio.Client {
	return defaultBackend.Client(Internal)
}

func GetInternalClient() *minio.Client {
	return internalCli()
}

func externalCli() *minio.Client {
	return defaultBackend.Client(External)
}

func expireDuration() time.Duration {
	return defaultBackend.ExpireTime()
}

func EnsureBucket(ctx context.Context, bucketName string) error {
	client := internalCli()
	if client == nil {
		return ErrUnavailable()
	}
	found, err := client.BucketExists(ctx, bucketName)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	return client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{})
}
