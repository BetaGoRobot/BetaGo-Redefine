package history

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestOriginalTextPreservesNegationMentionAndNewline(t *testing.T) {
	for _, tc := range []struct {
		kind, raw, want string
		ok              bool
	}{
		{"text", `{"text":"@_user_1 我没升级\n只是打算试试。"}`, "@_user_1 我没升级\n只是打算试试。", true},
		{"text", "我没有升级\n只是考虑。", "我没有升级\n只是考虑。", true},
		{"post", `{"title":"宽带","content":[[{"tag":"text","text":"网速快了"}],[{"tag":"text","text":"但公网地址没了"}]]}`, "宽带\n网速快了\n但公网地址没了", true},
		{"text", `{"image_key":"not-text"}`, "", false},
		{"image", `{"image_key":"picture"}`, "", false},
	} {
		got, ok := OriginalText(tc.kind, tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got %q,%v want %q,%v", tc.kind, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFilterMessageRetainsSeparateOriginalTextAndChatScope(t *testing.T) {
	source, err := json.Marshal(&xmodel.MessageIndex{MessageLog: &xmodel.MessageLog{ChatID: "source-chat", MessageType: "text", MessageID: "source-message", Mentions: "[]"}, RawMessage: "没升级\n只是想试试", OpenID: "human", CreateTimeV2: "2026-10-29T08:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	rows := FilterMessage(context.Background(), []opensearchapi.SearchHit{{Source: source}})
	if len(rows) != 1 || rows[0].ChatID != "source-chat" || rows[0].RawText == nil || *rows[0].RawText != "没升级\n只是想试试" {
		t.Fatalf("original evidence unavailable: %+v", rows)
	}
	if len(rows[0].MsgList) != 1 || rows[0].MsgList[0] != "没升级<换行>只是想试试" {
		t.Fatalf("legacy display changed: %+v", rows[0].MsgList)
	}
}
