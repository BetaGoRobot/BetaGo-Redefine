package miniodal

import (
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

var (
	clientInternal *minio.Client // for 内部使用, 连接minio服务的内网地址
	clientExternal *minio.Client // for 外部使用, 连接minio服务的公网地址，生成的预签名URL会使用这个client，以保证URL中使用公网地址
	expireTime     time.Duration
)

func Init(conf *config.MinioConfig) {
	var err error
	clientInternal, err = minio.New(conf.Internal.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(conf.AK, conf.SK, ""),
		Secure: conf.Internal.UseSSL,
	})
	if err != nil {
		logs.L().Panic("MinIO client initialization failed", zap.Error(err))
	}

	clientExternal, err = minio.New(conf.External.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(conf.AK, conf.SK, ""),
		Secure: conf.External.UseSSL,
	})
	if err != nil {
		logs.L().Panic("MinIO client initialization failed", zap.Error(err))
	}

	expireTime, err = time.ParseDuration(conf.ExpireTime)
	if err != nil {
		logs.L().Panic("Invalid expire time format", zap.Error(err))
	}

	logs.L().Info("MinIO clients initialized successfully")
}

func internalCli() *minio.Client {
	return clientInternal
}

func externalCli() *minio.Client {
	return clientExternal
}
