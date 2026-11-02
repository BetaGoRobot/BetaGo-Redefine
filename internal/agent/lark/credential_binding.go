package lark

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
)

const (
	luckinBindingActionPrefix = "luckin.personal."
	luckinBindingBindAction   = luckinBindingActionPrefix + "bind"
	luckinBindingRevokeAction = luckinBindingActionPrefix + "revoke"
	luckinBindingTokenField   = "luckin_personal_token"
	luckinBindingNamePrefix   = "luckin_personal_"
	luckinBindingMaxToken     = 8192
)

var errCredentialBinding = errors.New("personal credential binding failed")

// SendLuckinBinding sends an empty credential form visible only to the verified
// actor, within the task's existing group. The caller persists the opaque binding
// nonce before sending and pins this provider-confirmed message ID afterwards.
func (c *Client) SendLuckinBinding(ctx context.Context, scope domain.Scope, actor, bindingID string) (string, error) {
	if c.authorize(scope) != nil || !validBindingID(actor, 1024) || !validBindingID(bindingID, 256) {
		return "", errCredentialBinding
	}
	if ctx.Err() != nil {
		return "", errCredentialBinding
	}
	// The generated SDK has no ephemeral resource; Do retains its tenant-token,
	// timeout, response-byte-budget, closed-body and quiet-logging behavior.
	r, err := c.sdk.Do(ctx, &core.ApiReq{
		HttpMethod:                http.MethodPost,
		ApiPath:                   "/open-apis/ephemeral/v1/send",
		SupportedAccessTokenTypes: []core.AccessTokenType{core.AccessTokenTypeTenant},
		Body: map[string]any{
			"chat_id": scope.ChatID, "open_id": actor, "msg_type": "interactive",
			"card": luckinBindingCard(bindingID),
		},
	}, core.WithTenantKey(scope.TenantID))
	if err != nil || r == nil || r.StatusCode < 200 || r.StatusCode >= 300 {
		return "", errCredentialBinding
	}
	var receipt struct {
		Code *int `json:"code"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if json.Unmarshal(r.RawBody, &receipt) != nil || receipt.Code == nil || *receipt.Code != 0 || !validBindingID(receipt.Data.MessageID, 1024) {
		return "", errCredentialBinding
	}
	return receipt.Data.MessageID, nil
}

func validBindingID(id string, max int) bool {
	return len(id) > 0 && len(id) <= max && utf8.ValidString(id) && !strings.ContainsFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func luckinBindingCard(bindingID string) map[string]any {
	button := func(label, action, name string, submit bool) map[string]any {
		b := map[string]any{
			"tag": "button", "text": bindingPlainText(label), "name": name,
			"type":      "default",
			"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{"action": action, "binding_id": bindingID}}},
		}
		if submit {
			b["type"] = "primary"
			b["form_action_type"] = "submit"
		}
		return b
	}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"enable_forward": false},
		"header": map[string]any{"title": bindingPlainText("瑞幸个人凭证 · 仅对你可见"), "template": "blue"},
		"body": map[string]any{"elements": []any{
			map[string]any{"tag": "markdown", "content": "此卡片仅对你可见。请仅绑定本人账号；凭证会加密保存，仅供你本人使用。不要把凭证发送到群消息或交给其他人。\n前往[瑞幸开放平台](https://open.lkcoffee.com)获取本人的登录凭证，然后在下方填写。"},
			map[string]any{"tag": "form", "name": luckinBindingNamePrefix + "form", "elements": []any{
				map[string]any{"tag": "input", "name": luckinBindingTokenField, "required": true, "placeholder": bindingPlainText("粘贴本人的登录凭证"), "label": bindingPlainText("个人凭证")},
				button("加密保存个人凭证", luckinBindingBindAction, luckinBindingNamePrefix+"bind", true),
			}},
			button("撤销我的个人凭证", luckinBindingRevokeAction, luckinBindingNamePrefix+"revoke", false),
		}},
	}
}

func bindingPlainText(s string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": s}
}
