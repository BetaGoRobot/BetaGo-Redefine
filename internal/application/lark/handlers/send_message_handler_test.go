package handlers

import (
	"strings"
	"testing"
)

func TestSendMessageToolSpecGuidesMentionsSparingly(t *testing.T) {
	desc := SendMessage.ToolSpec().Desc
	for _, want := range []string{
		"<at user_id=\"open_id\">姓名</at>",
		"@姓名",
		"只有在需要某个具体成员响应",
		"普通群通知不要一上来就 @",
		"只是延续当前对话，不必为了点名而强行 @",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("desc = %q, want contain %q", desc, want)
		}
	}
}
