package schedule

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/minio/minio-go/v7"
	"go.uber.org/zap"
)

const (
	scheduleTaskResultBucket = "betago-schedule-results"
	scheduleTaskResultPrefix = "schedule-result://"
	scheduleTaskResultURLTTL = 5 * time.Minute
)

var scheduleTaskResultBucketReady atomic.Bool

func persistTaskResult(ctx context.Context, taskID, resultText string, finishedAt time.Time) string {
	trimmed := strings.TrimSpace(resultText)
	if trimmed == "" {
		return ""
	}

	objectKey := buildTaskResultObjectKey(taskID, finishedAt)
	if err := ensureScheduleTaskResultBucket(ctx); err != nil {
		logs.L().Ctx(ctx).Warn("ensure scheduled task result bucket failed",
			zap.Error(err),
			zap.String("bucket", scheduleTaskResultBucket))
		return trimmed
	}

	res := miniodal.New(miniodal.Internal).Upload(ctx).
		WithContentType(xmodel.ContentTypePlainText.String()).
		WithData([]byte(resultText)).
		Do(scheduleTaskResultBucket, objectKey, minio.PutObjectOptions{
			ContentType: xmodel.ContentTypePlainText.String(),
		})
	if err := res.Err(); err != nil {
		logs.L().Ctx(ctx).Warn("persist scheduled task result failed",
			zap.Error(err),
			zap.String("task_id", taskID),
			zap.String("object_key", objectKey))
		return trimmed
	}
	return buildTaskResultRef(objectKey)
}

func buildTaskResultLine(ctx context.Context, lastResult string) string {
	objectKey, ok := parseTaskResultRef(lastResult)
	if !ok {
		return ""
	}
	shortURL, err := resolveTaskResultShortURL(ctx, lastResult)
	if err == nil && shortURL != "" {
		return fmt.Sprintf("最近结果: [查看结果](%s)", shortURL)
	}
	return fmt.Sprintf("最近结果: `%s`", previewTaskResult(objectKey, 100))
}

func buildTaskResultObjectKey(taskID string, finishedAt time.Time) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		taskID = "unknown"
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	return fmt.Sprintf("task/%s/%s.txt", taskID, finishedAt.UTC().Format("20060102T150405.000000000Z"))
}

func buildTaskResultRef(objectKey string) string {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return ""
	}
	return scheduleTaskResultPrefix + objectKey
}

func parseTaskResultRef(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, scheduleTaskResultPrefix) {
		return "", false
	}
	objectKey := strings.TrimSpace(strings.TrimPrefix(value, scheduleTaskResultPrefix))
	if objectKey == "" {
		return "", false
	}
	return objectKey, true
}

func resolveTaskResultShortURL(ctx context.Context, resultRef string) (string, error) {
	objectKey, ok := parseTaskResultRef(resultRef)
	if !ok {
		return "", fmt.Errorf("invalid schedule task result ref")
	}
	shortURL, err := miniodal.PresignGetObjectShortURL(ctx, scheduleTaskResultBucket, objectKey, scheduleTaskResultURLTTL)
	if err != nil || strings.TrimSpace(shortURL) == "" {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("schedule task result not found")
	}
	return shortURL, nil
}

func ensureScheduleTaskResultBucket(ctx context.Context) error {
	if scheduleTaskResultBucketReady.Load() {
		return nil
	}
	if err := miniodal.EnsureBucket(ctx, scheduleTaskResultBucket); err != nil {
		return err
	}
	scheduleTaskResultBucketReady.Store(true)
	return nil
}

func previewTaskResult(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}
