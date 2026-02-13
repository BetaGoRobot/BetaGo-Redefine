package xmodel

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	ark_model "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type WordWithTag struct {
	Word string `json:"word"`
	Tag  string `json:"tag"`
}
type MessageIndex struct {
	*model.MessageLog
	ChatName             string          `json:"chat_name"`
	CreateTime           string          `json:"create_time"`
	CreateTimeV2         string          `json:"create_time_v2"`
	Message              []float32       `json:"message"`
	UserID               string          `json:"user_id"`
	UserName             string          `json:"user_name"`
	RawMessage           string          `json:"raw_message"`
	RawMessageJieba      string          `json:"raw_message_jieba"`
	RawMessageJiebaArray []string        `json:"raw_message_jieba_array"`
	RawMessageJiebaTag   []*WordWithTag  `json:"raw_message_jieba_tag"`
	TokenUsage           ark_model.Usage `json:"token_usage"`
	IsCommand            bool            `json:"is_command"`
	MainCommand          string          `json:"main_command"`
}

type CardActionIndex struct {
	*callback.CardActionTriggerEvent
	ChatName    string         `json:"chat_name"`
	CreateTime  string         `json:"create_time"`
	UserID      string         `json:"user_id"`
	UserName    string         `json:"user_name"`
	ActionValue map[string]any `json:"action_value"`
}
