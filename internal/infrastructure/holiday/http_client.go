package holiday

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

// httpClient 实现HTTP客户端
type httpclient struct {
	client *http.Client
}

func newHTTPClient() *httpclient {
	return &httpclient{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (h *httpclient) GetJSON(ctx context.Context, url string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 添加User-Agent等请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// 记录响应状态和内容用于调试
	logs.L().Debug("Holiday API response",
		zap.String("url", url),
		zap.Int("status", resp.StatusCode),
		zap.Int("body_len", len(body)))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		logs.L().Debug("Failed to unmarshal response",
			zap.String("url", url),
			zap.String("body", string(body)),
			zap.Error(err))
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}
