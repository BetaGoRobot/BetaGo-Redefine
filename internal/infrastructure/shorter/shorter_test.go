package shorter

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

func TestGenAKAKutt(t *testing.T) {
	otel.Init(config.Get().OtelConfig)
	logs.Init()
	u := &url.URL{
		Scheme: "https",
		Host:   "beta.betagov.cn",
		Path:   "/api/v1/oss/object",
	}
	GenAKAKutt1(context.Background(), u, ExpireTime{1, TimeUnitsMinute})
}

func GenAKAKutt1(ctx context.Context, u *url.URL, expires ExpireTime) (newURL *url.URL) {
	oldURL := u.String()
	req := &KuttRequest{
		Target:   oldURL,
		ExpireIn: fmt.Sprintf("%d%s", expires.Value, expires.Unit),
		Reuse:    true,
	}
	reqBody, err := sonic.Marshal(req)
	if err != nil {
		logs.L().Ctx(ctx).Error("Marshal failed", zap.Error(err))
		return
	}
	r, err := xhttp.HttpClient.R().
		SetHeader("X-API-KEY", config.Get().KuttConfig.APIKey).
		SetHeader("Content-Type", "application/json").
		SetBody(reqBody).
		Post("https://" + config.Get().KuttConfig.BaseURL + "/api/links")
	if err != nil || (r.StatusCode() != 200 && r.StatusCode() != 201) {
		logs.L().Ctx(ctx).Error("Post failed", zap.Error(err), zap.Int("status_code", r.StatusCode()), zap.String("body", string(r.Body())))
		return
	}
	resp := &KuttResp{}
	err = sonic.Unmarshal(r.Body(), resp)
	if err != nil {
		logs.L().Ctx(ctx).Error("Unmarshal failed", zap.Error(err))
		return
	}
	newURL, err = url.Parse(resp.Link)
	if err != nil {
		logs.L().Ctx(ctx).Error("Parse url failed", zap.Error(err))
		return
	}
	newURL.Host = config.Get().KuttConfig.ExternalURL // ==> 443被封，替换一下。
	logs.L().Ctx(ctx).Debug("GenAKA with url", zap.String("new_url", newURL.String()), zap.String("old_url", oldURL))
	return
}
