package utils

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func GetIfInthread(ctx context.Context, meta *xhandler.BaseMetaData, sceneDefault bool) bool {
	if meta != nil {
		if mode, ok := meta.GetExtra("interaction_mode"); ok && mode == "agentic" {
			return !meta.IsP2P
		}
	}
	if !sceneDefault { // 如果默认就是不开话题，就直接回复
		return false
	}
	if meta == nil {
		return false
	}
	return !meta.IsP2P // 如果默认不是要发的，且是私聊，那就直接发非话题吧
}
