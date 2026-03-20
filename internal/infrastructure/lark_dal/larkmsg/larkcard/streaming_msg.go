package larkcard

import "github.com/bytedance/sonic"

type CardEntitySendContent struct {
	Type string              `json:"type"`
	Data *CardEntitySendData `json:"data"`
}

type CardEntitySendData struct {
	CardID string `json:"card_id"`
}

type CardStreamingSettings struct {
	Config struct {
		StreamingMode bool `json:"streaming_mode"`
	} `json:"config"`
}

func EnableCardStreaming() *CardStreamingSettings {
	return newCardStreamingSettings(true)
}

func DisableCardStreaming() *CardStreamingSettings {
	return newCardStreamingSettings(false)
}

func newCardStreamingSettings(enabled bool) *CardStreamingSettings {
	return &CardStreamingSettings{
		struct {
			StreamingMode bool "json:\"streaming_mode\""
		}{
			enabled,
		},
	}
}

func (s *CardStreamingSettings) String() string {
	ss, _ := sonic.MarshalString(s)
	return ss
}

func NewCardEntityContent(cardID string) *CardEntitySendContent {
	return &CardEntitySendContent{
		"card",
		&CardEntitySendData{
			cardID,
		},
	}
}

func (e *CardEntitySendContent) String() string {
	s, _ := sonic.MarshalString(e)
	return s
}
