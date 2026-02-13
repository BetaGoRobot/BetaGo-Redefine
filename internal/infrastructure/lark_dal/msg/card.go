package msg

import (
	"context"

	"github.com/BetaGoRobot/BetaGo/utility/otel"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
)

type CardBuilderBase struct {
	Title    string
	SubTitle string
	Content  string
}

func NewCardBuildHelper() *CardBuilderBase {
	return &CardBuilderBase{}
}

func (h *CardBuilderBase) SetTitle(title string) *CardBuilderBase {
	h.Title = title
	return h
}

func (h *CardBuilderBase) SetSubTitle(subTitle string) *CardBuilderBase {
	h.SubTitle = subTitle
	return h
}

func (h *CardBuilderBase) SetContent(text string) *CardBuilderBase {
	h.Content = text
	return h
}

func (h *CardBuilderBase) Build(ctx context.Context) *TemplateCardContent {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	cardContent := NewCardContent(ctx, NormalCardReplyTemplate)
	return cardContent.
		AddVariable(
			"title", h.Title,
		).
		AddVariable(
			"subtitle", h.SubTitle,
		).
		AddVariable(
			"content", h.Content,
		)
}

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

func DisableCardStreaming() *CardStreamingSettings {
	return &CardStreamingSettings{
		struct {
			StreamingMode bool "json:\"streaming_mode\""
		}{
			false,
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
