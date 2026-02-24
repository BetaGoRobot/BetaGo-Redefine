package neteaseapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/gotify"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xrequest"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	"github.com/minio/minio-go/v7"
	"go.uber.org/zap"
)

// RefreshLogin 刷新登录
//
//	@receiver ctx
//	@return error
func (neteaseCtx *NetEaseContext) RefreshLogin(ctx context.Context) error {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	logs.L().Ctx(ctx).Info("RefreshLogin...")

	resp, err := xhttp.HttpClient.R().
		SetCookies(neteaseCtx.cookies).
		Post(NetEaseAPIBaseURL + "/login/refresh")

	if err != nil || (resp != nil && resp.StatusCode() != 200) {
		logs.L().Ctx(ctx).Error("error in request refresh login", zap.Error(err))
		return err
	}
	respMap := make(map[string]interface{})
	err = sonic.Unmarshal(resp.Body(), &respMap)
	if err != nil {
		return err
	}

	if code, ok := respMap["code"]; ok {
		if code != 200 {
			return fmt.Errorf("RefreshLogin error, with msg %v", respMap["msg"])
		}
	}

	if neteaseCtx.cookies == nil {
		neteaseCtx.cookies = make([]*http.Cookie, 0)
	}
	newCookies := resp.Cookies()
	if len(newCookies) > 0 {
		neteaseCtx.cookies = newCookies
		neteaseCtx.SaveCookie(ctx)
	}

	return err
}

func (neteaseCtx *NetEaseContext) GetUniKey(ctx context.Context) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	logs.L().Ctx(ctx).Info("getUniKey...", zap.String("traceID", span.SpanContext().TraceID().String()))

	resp, err := xrequest.Req().Post(NetEaseAPIBaseURL + "/login/qr/key")
	if err != nil || resp.StatusCode() != 200 {
		if err == nil {
			err = fmt.Errorf("LoginNetEaseQR error, StatusCode %d", resp.StatusCode())
		}
		return
	}
	data := resp.Body()
	respMap := make(map[string]interface{})
	if err = sonic.Unmarshal(data, &respMap); err != nil {
		return
	}
	neteaseCtx.qrStruct.uniKey = respMap["data"].(map[string]interface{})["unikey"].(string)
	return
}

func (neteaseCtx *NetEaseContext) GetQRBase64(ctx context.Context) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	logs.L().Ctx(ctx).Info("getQRBase64...", zap.String("traceID", span.SpanContext().TraceID().String()))

	resp, err := xrequest.
		ReqTimestamp().
		SetFormDataFromValues(
			map[string][]string{
				"key":   {neteaseCtx.qrStruct.uniKey},
				"qrimg": {"1"},
			}).
		Post(NetEaseAPIBaseURL + "/login/qr/create")
	if err != nil || resp.StatusCode() != 200 {
		if err == nil {
			err = fmt.Errorf("LoginNetEaseQR error, StatusCode %d", resp.StatusCode())
		}
		return
	}
	data := (resp.Body())
	respMap := make(map[string]interface{})
	if err = sonic.Unmarshal(data, &respMap); err != nil {
		return
	}
	neteaseCtx.qrStruct.qrBase64 = respMap["data"].(map[string]interface{})["qrimg"].(string)
	return
}

func (neteaseCtx *NetEaseContext) checkQRStatus(ctx context.Context) (err error) {
	if !neteaseCtx.qrStruct.isOutDated {
		once := &sync.Once{}
		for {

			time.Sleep(time.Second * 2)
			resp, err := xhttp.HttpClient.R().
				SetFormData(map[string]string{"key": neteaseCtx.qrStruct.uniKey}).
				SetQueryParam("timestamp", fmt.Sprint(time.Now().Unix())).
				SetContext(ctx).
				Post(NetEaseAPIBaseURL + "/login/qr/check")
			if err != nil {
				return err
			}
			data := resp.Body()
			respMap := make(map[string]interface{})
			if err = sonic.Unmarshal(data, &respMap); err != nil {
				return err
			}
			switch respMap["code"].(float64) {
			case 801:
				once.Do(func() { logs.L().Ctx(ctx).Info("Waiting for scan") })
			case 800:
				once.Do(func() {
					logs.L().Ctx(ctx).Info("[neteaseQR]二维码已失效")
					neteaseCtx.qrStruct.isOutDated = true
				})
				return err
			case 802:
				once.Do(func() { logs.L().Ctx(ctx).Info("[neteaseQR]扫描未确认") })
			case 803:
				logs.L().Ctx(ctx).Info("[neteaseQR]登陆成功！")
				neteaseCtx.cookies = resp.Cookies()
				neteaseCtx.SaveCookie(ctx)
				neteaseCtx.loginType = "qr"
				gotify.SendMessage(ctx, "网易云登录", "登陆成功！", 7)
				return nil
			}
		}
	}
	return
}

// LoginNetEaseQR 通过二维码获取登陆Cookie
//
//	@receiver ctx
//	@return err
func (neteaseCtx *NetEaseContext) LoginNetEaseQR(ctx context.Context) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	logs.L().Ctx(ctx).Info("LoginNetEaseQR...", zap.String("traceID", span.SpanContext().TraceID().String()))
	neteaseCtx.GetUniKey(ctx)

	neteaseCtx.GetQRBase64(ctx)
	linkURL, err := miniodal.New(miniodal.Internal).Upload(ctx).
		WithContentType(xmodel.ContentTypeImgPNG.String()).
		WithReader(qrImgReadCloser(ctx, neteaseCtx.qrStruct.qrBase64)).
		Do("cloudmusic", "QRCode/"+strconv.Itoa(int(time.Now().Unix()))+".png", minio.PutObjectOptions{}).
		PreSignURL()
	if err != nil {
		logs.L().Ctx(ctx).Error("upload QRCode failed", zap.Error(err))
		return err
	}

	gotify.SendMessage(ctx, "网易云登录", fmt.Sprintf("![QRCode](%s)", linkURL), 7)
	neteaseCtx.checkQRStatus(ctx)
	return
}

func qrImgReadCloser(ctx context.Context, imgBase64 string) (r io.ReadCloser) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	i := strings.Index(imgBase64, ",") // string is img/png;base64,xxx
	d := base64.NewDecoder(base64.StdEncoding, strings.NewReader(imgBase64[i+1:]))

	return io.NopCloser(d)
}

// LoginNetEase 获取登陆Cookie
//
//	@receiver ctx
//	@return err
func (neteaseCtx *NetEaseContext) LoginNetEase(ctx context.Context) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	logs.L().Ctx(ctx).Info("LoginNetEase...")

	// !Step1:检查登陆状态
	if neteaseCtx.CheckIfLogin(ctx) {
		logs.L().Ctx(ctx).Info("Already login")
		if neteaseCtx.loginType != "qr" {
			// 已登陆，刷新登陆
			err = neteaseCtx.RefreshLogin(ctx)
		}
		return
	}
	conf := config.Get().NeteaseMusicConfig
	if phoneNum, password := conf.UserName, conf.PassWord; phoneNum == "" && password == "" {
		logs.L().Ctx(ctx).Info("Empty NetEase account and password")
		return
	}
	// !Step2:未登陆，启动登陆
	resp, err := xhttp.HttpClient.R().
		SetCookies(neteaseCtx.cookies).
		SetFormData(
			map[string]string{
				"email":    conf.UserName,
				"password": conf.PassWord,
			},
		).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().Unix())).
		Post(NetEaseAPIBaseURL + "/login")
	if err != nil || resp.StatusCode() != 200 {
		if err == nil {
			err = fmt.Errorf("LoginNetEase error, with msg %v, StatusCode %d", string(resp.Body()), resp.StatusCode())
		}
		return
	}
	neteaseCtx.cookies = resp.Cookies()
	neteaseCtx.SaveCookie(ctx)
	return
}

// CheckIfLogin 检查是否登陆
//
//	@receiver ctx
//	@return bool
func (neteaseCtx *NetEaseContext) CheckIfLogin(ctx context.Context) bool {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	logs.L().Ctx(ctx).Info("ChekIfLogin...")

	resp, err := xhttp.HttpClient.R().
		SetCookies(neteaseCtx.cookies).
		SetContext(ctx).
		SetQueryParam("timestamp", fmt.Sprint(time.Now().UnixNano())).
		Get(NetEaseAPIBaseURL + "/login/status")
	if err != nil || resp.StatusCode() != 200 {
		logs.L().Ctx(ctx).Error("error in request login status", zap.Error(err))
		return false
	}
	data := resp.Body()
	loginStatus := LoginStatusStruct{}
	if err = sonic.Unmarshal(data, &loginStatus); err != nil {
		logs.L().Ctx(ctx).Error("error in unmarshal loginStatus", zap.Error(err))
	} else {
		if anonimousUser, ok := loginStatus.Data.Account["anonimousUser"].(bool); ok && anonimousUser {
			return false
		} else if loginStatus.Data.Account != nil {
			return true
		}
		return false
	}

	return false
}

// TryGetLastCookie 获取初始化Cookie
//
//	@receiver ctx
func (neteaseCtx *NetEaseContext) TryGetLastCookie(ctx context.Context) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()

	f, err := os.Open("/data/last_cookie.json")
	if err != nil {
		logs.L().Ctx(ctx).Error("error in open last_cookie.json", zap.Error(err))
		return
	}
	defer f.Close()
	cookieData := make([]byte, 0)
	cookieData, err = io.ReadAll(f)
	if len(cookieData) == 0 {
		logs.L().Ctx(ctx).Info("No cookieData, skip json marshal")
		return
	}
	cookie := make(map[string]string)

	if err = sonic.Unmarshal(cookieData, &cookie); err != nil {
		logs.L().Ctx(ctx).Error("error in unmarshal cookieData", zap.Error(err))
	}
	for k, v := range cookie {
		neteaseCtx.cookies = append(neteaseCtx.cookies, &http.Cookie{Name: k, Value: v})
	}
	neteaseCtx.loginType = "qr"
}

// SaveCookie 保存Cookie
//
//	@receiver ctx
func (neteaseCtx *NetEaseContext) SaveCookie(ctx context.Context) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	if neteaseCtx.cookies == nil && len(neteaseCtx.cookies) == 0 {
		return
	}
	f, err := os.OpenFile("/data/last_cookie.json", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		logs.L().Ctx(ctx).Error("error in open last_cookie.json", zap.Error(err))
		return
	}
	defer f.Close()

	toWriteMap := make(map[string]string)
	for _, cookie := range neteaseCtx.cookies {
		toWriteMap[cookie.Name] = cookie.Value
	}
	cookieData, err := sonic.Marshal(toWriteMap)
	if err != nil {
		logs.L().Ctx(ctx).Error("Unknown error", zap.Error(err))
	}
	f.Write(cookieData)
}
