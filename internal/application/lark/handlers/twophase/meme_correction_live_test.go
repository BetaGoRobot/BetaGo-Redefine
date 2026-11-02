package twophase

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	cfg "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

// Exercises the actual master tool planner without starting any receiver,
// corpus writer or chat sender. It does not claim final-generator acceptance.
func TestMemeCorrectionMasterLivePlanner(t *testing.T) {
	if os.Getenv("MEME_CORRECTION_MASTER_LIVE") != "1" {
		t.Skip("explicit paid planner probe only")
	}
	c, err := cfg.LoadFileE(os.Getenv("MEME_CORRECTION_MASTER_CONFIG"))
	if err != nil {
		t.Fatal("readonly master configuration unavailable")
	}
	ark_dal.Init(c.ArkConfig)
	var rows []map[string]any
	t.Cleanup(func() {
		b, e := json.MarshalIndent(rows, "", "  ")
		if e != nil || os.WriteFile(os.Getenv("MEME_CORRECTION_OUTPUT"), append(b, '\n'), 0600) != nil {
			t.Error("cannot save first planner responses")
		}
	})
	for _, tc := range []struct {
		ID, Text string
		Web      bool
	}{
		{"explicit", "你没理解这个梗，查下钢铁雄心和海岛奇兵在这里的关系。", true},
		{"rejected", "不是这个意思，你把海岛奇兵只当游戏了，这个梗召回错了。", true},
		{"stop", "回答太长了，别回了。", false},
		{"no_web", "这个梗你理解错了，但不要联网，我只是说现在只玩海岛奇兵了。", false},
		{"quoted", "帮我把这句原样加引号：你没理解这个梗，召回错了。", false},
	} {
		t.Run(tc.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			hints, e := PlanToolsWithContext(ctx, "", "", c.ArkConfig.LiteModel, tc.Text, []string{"用户：三年，从钢铁雄心玩成海岛奇兵了。", "机器人：海岛奇兵就是个经营海岛的游戏，换个游戏继续打。"}, PlannerMessageContext{Direct: true}, llmusage.Scope{})
			rows = append(rows, map[string]any{"id": tc.ID, "feedback": tc.Text, "hints": hints, "expect_web": tc.Web, "model": c.ArkConfig.LiteModel, "error": e != nil})
			if e != nil {
				t.Fatal("native master planner failed")
			}
			web := false
			for _, hint := range hints {
				if hint == intentmeta.ToolHint("web_search") {
					web = true
				}
			}
			if web != tc.Web {
				t.Error("unexpected first-response web_search hint")
			}
		})
	}
}
