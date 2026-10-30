package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/cloudwego/eino/schema"
)

func TestSceneUsesExplicitCurrentBeijingTime(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	prompt, _, _, err := f.engine.prompt(context.Background(), f.task)
	if err != nil {
		t.Fatal(err)
	}
	var scene struct {
		CurrentTime      time.Time
		Timezone         string
		CurrentUtterance struct{ OccurredAt time.Time }
		Attention        struct{ AsOf, WindowStart time.Time }
	}
	if err := json.Unmarshal([]byte(prompt), &scene); err != nil {
		t.Fatal(err)
	}
	if scene.Timezone != "Asia/Shanghai (UTC+08:00)" || time.Since(scene.CurrentTime).Abs() > time.Minute {
		t.Fatalf("current time invalid: %s %s", scene.CurrentTime, scene.Timezone)
	}
	for _, at := range []time.Time{scene.CurrentTime, scene.CurrentUtterance.OccurredAt, scene.Attention.AsOf, scene.Attention.WindowStart} {
		_, offset := at.Zone()
		if offset != 8*3600 {
			t.Errorf("non-Beijing timestamp %s", at)
		}
	}
}

type clockCapture struct{ request modelapi.ChatRequest }

func (c *clockCapture) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	c.request = r
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "ok"}}, nil
}
func TestEveryModelRequestRefreshesBeijingClockIncludingResume(t *testing.T) {
	c := &clockCapture{}
	adapter := NewChatAdapter(c, 1<<20)
	for _, in := range [][]*schema.Message{{schema.UserMessage("你好")}, {schema.SystemMessage("历史指令\n[当前时间] 2000-01-01T00:00:00Z"), schema.UserMessage("继续")}} {
		if _, err := adapter.Generate(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		last := c.request.Messages[len(c.request.Messages)-1]
		// A fresh system clock message must remain separate from saved scene facts.
		found := false
		for _, msg := range c.request.Messages {
			text, _ := msg.Content.(string)
			if msg.Role == "system" && strings.Contains(text, "北京时间") && strings.Contains(text, "+08:00") && strings.Contains(text, time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")) {
				found = true
			}
		}
		if !found {
			t.Errorf("request missing fresh Beijing clock; last role=%s", last.Role)
		}
		if len(in) == 2 && in[0].Content != "历史指令\n[当前时间] 2000-01-01T00:00:00Z" {
			t.Fatal("mutated saved checkpoint")
		}
	}
}
