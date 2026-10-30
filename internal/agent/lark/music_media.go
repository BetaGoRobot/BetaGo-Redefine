package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	auth "github.com/larksuite/oapi-sdk-go/v3/service/auth/v3"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// UploadMusicImage accepts provider cover bytes already validated by the music
// service. It authorizes the server-owned group scope and returns only a real
// provider image handle. Provider covers are not Lark message source references.
func (c *Client) UploadMusicImage(ctx context.Context, s domain.Scope, b []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.authorize(s) != nil {
		return "", domain.ErrUnauthorized
	}
	if len(b) == 0 || int64(len(b)) > maxImageBytes {
		return "", domain.ErrInvalid
	}
	// Explicitly acquire this app's token and pass it to the upload request, keeping
	// this path independent from the SDK's process-global legacy token cache.
	token, err := c.sdk.Auth.V3.TenantAccessToken.Internal(ctx, auth.NewInternalTenantAccessTokenReqBuilder().Body(auth.NewInternalTenantAccessTokenReqBodyBuilder().AppId(c.cfg.AppID).AppSecret(c.cfg.AppSecret).Build()).Build())
	if err != nil || token == nil {
		return "", communityAPIError(ctx, err, 0, 0)
	}
	var credentials struct {
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if token.ApiResp == nil || json.Unmarshal(token.RawBody, &credentials) != nil {
		return "", errAPI
	}
	if token.StatusCode != http.StatusOK || !token.Success() || credentials.TenantAccessToken == "" || strings.ContainsAny(credentials.TenantAccessToken, "\r\n") {
		return "", errAPI
	}
	r, err := c.sdk.Im.Image.Create(ctx, im.NewCreateImageReqBuilder().Body(im.NewCreateImageReqBodyBuilder().ImageType("message").Image(bytes.NewReader(b)).Build()).Build(), core.WithTenantKey(s.TenantID), core.WithTenantAccessToken(credentials.TenantAccessToken))
	if err != nil || r == nil {
		return "", communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() || r.StatusCode != http.StatusOK {
		return "", communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	if r.Data == nil || strings.TrimSpace(value(r.Data.ImageKey)) == "" {
		return "", errAPI
	}
	return value(r.Data.ImageKey), nil
}
