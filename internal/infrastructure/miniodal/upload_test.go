package miniodal

import (
	"context"
	"fmt"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/minio/minio-go/v7"
)

func TestUploadFile(t *testing.T) {
	config := config.LoadFile("../../../.dev/config.toml")
	otel.Init(config.OtelConfig)
	logs.Init()
	Init(config.MinioConfig)
	ctx := context.Background()
	dal := New(Internal)
	url, err := dal.Upload(ctx).WithData([]byte("test data")).Do(
		"tmp", "test_0212-14.txt", minio.PutObjectOptions{},
	).PreSignURL()
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	fmt.Println(url)
	url, err = dal.Upload(ctx).SkipDedup(true).WithData([]byte("test data123456")).Do(
		"tmp", "test_0212-14.txt", minio.PutObjectOptions{},
	).PreSignURL()
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	fmt.Println(url)
}
