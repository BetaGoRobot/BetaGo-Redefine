package hitokoto

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xrequest"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

const (
	hitokotoURL = "https://v1.hitokoto.cn"
)

// RespBody  一言返回体
type RespBody struct {
	ID         int         `json:"id"`
	UUID       string      `json:"uuid"`
	Hitokoto   string      `json:"hitokoto"`
	Type       string      `json:"type"`
	From       string      `json:"from"`
	FromWho    interface{} `json:"from_who"`
	Creator    string      `json:"creator"`
	CreatorUID int         `json:"creator_uid"`
	Reviewer   int         `json:"reviewer"`
	CommitFrom string      `json:"commit_from"`
	CreatedAt  string      `json:"created_at"`
	Length     int         `json:"length"`
}

// GetHitokoto 获取一言
//
//	@param parameters
func GetHitokoto(field ...string) (hitokotoRes RespBody, err error) {
	resp, err := xrequest.Req().SetQueryParamsFromValues(map[string][]string{"c": field}).Get(hitokotoURL)
	if err != nil {
		logs.L().Error("获取一言失败", zap.Error(err))
		return
	}
	if err = sonic.Unmarshal(resp.Body(), &hitokotoRes); err != nil {
		logs.L().Error("获取一言失败", zap.Error(err))
		return
	}
	return
}
