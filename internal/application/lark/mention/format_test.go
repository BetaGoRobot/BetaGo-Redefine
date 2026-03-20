package mention

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestNormalizeTextWithMentionsReplacesRawAtMentions(t *testing.T) {
	name := "张三"
	openID := "ou_zhangsan"
	got := normalizeTextWithMentions("提醒 @张三 下午开会", nil, map[string]*larkim.ListMember{
		openID: {
			MemberId: &openID,
			Name:     &name,
		},
	})
	want := "提醒 <at user_id=\"ou_zhangsan\">张三</at> 下午开会"
	if got != want {
		t.Fatalf("normalizeOutgoingTextWithMembers() = %q, want %q", got, want)
	}
}

func TestNormalizeTextWithMentionsPreservesExistingLarkAtFormat(t *testing.T) {
	name := "张三"
	openID := "ou_zhangsan"
	text := "提醒 <at user_id=\"ou_zhangsan\">张三</at> 下午开会"
	got := normalizeTextWithMentions(text, nil, map[string]*larkim.ListMember{
		openID: {
			MemberId: &openID,
			Name:     &name,
		},
	})
	if got != text {
		t.Fatalf("normalizeTextWithMentions() = %q, want unchanged %q", got, text)
	}
}

func TestNormalizeTextWithMentionsUsesHistoryMentionsAsFallback(t *testing.T) {
	name := "李四"
	openID := "ou_lisi"
	got := normalizeTextWithMentions("提醒 @李四 跟进", history.OpensearchMsgLogList{
		{
			UserName: "王五",
			OpenID:   "ou_wangwu",
			MentionList: []*larkim.Mention{
				{
					Name: &name,
					Id:   &openID,
				},
			},
		},
	}, nil)
	want := "提醒 <at user_id=\"ou_lisi\">李四</at> 跟进"
	if got != want {
		t.Fatalf("normalizeTextWithMentions() = %q, want %q", got, want)
	}
}
