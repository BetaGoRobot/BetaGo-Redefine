package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	discoverCardComponentsResultKey = "discover_card_components_result"
	composeCardResultKey            = "compose_card_result"
)

type discoverCardComponentsHandler struct {
	service agentcardtool.Service
}

type composeCardHandler struct {
	service agentcardtool.Service
}

func registerAgentCardTools(
	registry *arktools.Impl[larkim.P2MessageReceiveV1],
	service agentcardtool.Service,
) {
	if registry == nil || service == nil {
		return
	}
	xcommand.RegisterTool(
		registry,
		discoverCardComponentsHandler{service: service},
	)
	xcommand.RegisterTool(registry, composeCardHandler{service: service})
}

func (h discoverCardComponentsHandler) ParseTool(
	raw string,
) (agentcardtool.DiscoverRequest, error) {
	var request agentcardtool.DiscoverRequest
	if err := decodeAgentCardToolInput(raw, &request); err != nil {
		return agentcardtool.DiscoverRequest{}, err
	}
	return request, nil
}

func (h discoverCardComponentsHandler) Handle(
	ctx context.Context,
	_ *larkim.P2MessageReceiveV1,
	meta *xhandler.BaseMetaData,
	request agentcardtool.DiscoverRequest,
) error {
	if h.service == nil || meta == nil {
		return errors.New("agent card component discovery is not configured")
	}
	response, err := h.service.DiscoverComponents(ctx, request)
	if err != nil {
		return err
	}
	meta.SetExtra(
		discoverCardComponentsResultKey,
		agentcardtool.MarshalResponse(response),
	)
	return nil
}

func (discoverCardComponentsHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "discover_card_components",
		Desc: "Discover the current semantic Agent Card component catalog by optional version, category, or name. Returns safe fields, limits, examples, and lifecycle compatibility without runtime tokens, callback envelopes, or capability arguments.",
		Params: arktools.NewParams("object").
			AddProp("version", &arktools.Prop{
				Type: "string", Desc: "Card DSL version; defaults to agent-card/v1",
				Enum: []any{"agent-card/v1"},
			}).
			AddProp("category", &arktools.Prop{
				Type: "string", Desc: "Optional component category",
				Enum: []any{"layout", "content", "input", "action"},
			}).
			AddProp("name", &arktools.Prop{
				Type: "string", Desc: "Optional exact component name",
			}),
		Result: func(meta *xhandler.BaseMetaData) string {
			result, _ := meta.GetExtra(discoverCardComponentsResultKey)
			return result
		},
	}
}

func (h composeCardHandler) ParseTool(
	raw string,
) (agentcardtool.ComposeRequest, error) {
	var request agentcardtool.ComposeRequest
	if err := decodeAgentCardToolInput(raw, &request); err != nil {
		return agentcardtool.ComposeRequest{}, err
	}
	return request, nil
}

func (h composeCardHandler) Handle(
	ctx context.Context,
	data *larkim.P2MessageReceiveV1,
	meta *xhandler.BaseMetaData,
	request agentcardtool.ComposeRequest,
) error {
	if h.service == nil || meta == nil {
		return errors.New("agent card composition is not configured")
	}
	messageID := currentMessageID(data)
	response, err := h.service.ComposeCard(
		ctx,
		agentcardtool.ComposeContext{
			ChatID:           currentChatID(data, meta),
			ActorOpenID:      currentOpenID(data, meta),
			ReplyToMessageID: messageID,
			TriggerEventID:   messageID,
		},
		request,
	)
	if err != nil {
		return err
	}
	meta.SetExtra(composeCardResultKey, agentcardtool.MarshalResponse(response))
	return nil
}

func (composeCardHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "compose_card",
		Desc: "Compose and send a typed semantic Agent Card only when interaction or information hierarchy materially benefits. Prefer text for simple answers. Never request secrets, passwords, tokens, OTPs, identity, or payment data. Choose the action mode that matches the interaction. The sent card is the response; avoid a duplicate post-card summary.",
		Params: arktools.NewParams("object").
			AddProp("purpose", &arktools.Prop{
				Type: "string", Desc: "Concise semantic purpose such as confirmation or collect_reason",
			}).
			AddProp("card", cardToolCardSchema()).
			AddProp("interaction", &arktools.Prop{
				Type: "object", Desc: "Durable interaction behavior",
				Props: map[string]*arktools.Prop{
					"mode": {
						Type: "string", Desc: "Mode shared by the card actions",
						Enum: []any{"ui_action", "capability_confirm", "server_action"},
					},
					"expires_in_seconds": {
						Type: "integer", Desc: "Interaction expiry from 60 to 3600 seconds",
					},
				},
				Required: []string{"mode"},
			}).
			AddRequired("purpose").
			AddRequired("card").
			AddRequired("interaction"),
		Result: func(meta *xhandler.BaseMetaData) string {
			result, _ := meta.GetExtra(composeCardResultKey)
			return result
		},
	}
}

func cardToolCardSchema() *arktools.Prop {
	return &arktools.Prop{
		Type: "object", Desc: "Typed semantic CardSpec; raw Lark JSON is not accepted",
		Props: map[string]*arktools.Prop{
			"version": {
				Type: "string", Desc: "Card DSL version",
				Enum: []any{"agent-card/v1"},
			},
			"title": {Type: "string", Desc: "Card title"},
			"theme": {
				Type: "string", Desc: "Semantic theme",
				Enum: []any{"", "blue", "green", "orange", "red", "grey"},
			},
			"blocks": {
				Type: "array", Desc: "Semantic content and input blocks",
				Items: cardToolBlockSchema(1),
			},
			"actions": {
				Type: "array", Desc: "Semantic actions",
				Items: cardToolActionSchema(),
			},
			"meta": {
				Type: "object", Desc: "Public semantic metadata",
				Props: map[string]*arktools.Prop{
					"purpose": {Type: "string", Desc: "Public card purpose"},
					"summary": {Type: "string", Desc: "Concise public summary"},
					"labels": {
						Type: "array", Desc: "Public labels",
						Items: &arktools.Prop{Type: "string", Desc: "Label"},
					},
					"locale": {Type: "string", Desc: "Content locale"},
				},
			},
		},
		Required: []string{"title", "blocks"},
	}
}

func cardToolBlockSchema(depth int) *arktools.Prop {
	props := map[string]*arktools.Prop{
		"kind": {
			Type: "string", Desc: "Semantic block kind",
			Enum: []any{
				"markdown", "plain_text", "facts", "note", "divider",
				"columns", "section", "text_input", "single_select",
				"multi_select",
			},
		},
		"id":    {Type: "string", Desc: "Stable semantic component id"},
		"text":  {Type: "string", Desc: "Semantic text content"},
		"title": {Type: "string", Desc: "Section title"},
		"items": {
			Type: "array", Desc: "Fact items",
			Items: &arktools.Prop{
				Type: "object", Desc: "Fact",
				Props: map[string]*arktools.Prop{
					"label": {Type: "string", Desc: "Fact label"},
					"value": {Type: "string", Desc: "Fact value"},
				},
				Required: []string{"label", "value"},
			},
		},
		"field_id":    {Type: "string", Desc: "Submitted field id"},
		"form_id":     {Type: "string", Desc: "Form group id"},
		"label":       {Type: "string", Desc: "Visible input label"},
		"required":    {Type: "boolean", Desc: "Whether input is required"},
		"purpose":     {Type: "string", Desc: "Non-sensitive input purpose"},
		"placeholder": {Type: "string", Desc: "Non-sensitive input hint"},
		"multiline":   {Type: "boolean", Desc: "Allow multiple lines"},
		"min_length":  {Type: "integer", Desc: "Minimum text length"},
		"max_length":  {Type: "integer", Desc: "Maximum text length"},
		"options": {
			Type: "array", Desc: "Bounded select options",
			Items: &arktools.Prop{
				Type: "object", Desc: "Select option",
				Props: map[string]*arktools.Prop{
					"label": {Type: "string", Desc: "Visible option label"},
					"value": {Type: "string", Desc: "Stable option value"},
				},
				Required: []string{"label", "value"},
			},
		},
	}
	if depth < 3 {
		props["blocks"] = &arktools.Prop{
			Type: "array", Desc: "Nested section blocks",
			Items: cardToolBlockSchema(depth + 1),
		}
		props["columns"] = &arktools.Prop{
			Type: "array", Desc: "Two or three columns",
			Items: &arktools.Prop{
				Type: "object", Desc: "Column",
				Props: map[string]*arktools.Prop{
					"id":    {Type: "string", Desc: "Column id"},
					"width": {Type: "integer", Desc: "Relative width"},
					"blocks": {
						Type: "array", Desc: "Column blocks",
						Items: cardToolBlockSchema(depth + 1),
					},
				},
				Required: []string{"id", "blocks"},
			},
		}
	}
	return &arktools.Prop{
		Type: "object", Desc: "Semantic block", Props: props,
		Required: []string{"kind", "id"},
	}
}

func cardToolActionSchema() *arktools.Prop {
	return &arktools.Prop{
		Type: "object", Desc: "Semantic action",
		Props: map[string]*arktools.Prop{
			"kind": {
				Type: "string", Desc: "Action kind",
				Enum: []any{"button", "submit", "reset", "cancel"},
			},
			"id":    {Type: "string", Desc: "Stable action id"},
			"label": {Type: "string", Desc: "Visible action label"},
			"style": {
				Type: "string", Desc: "Semantic emphasis",
				Enum: []any{"", "default", "primary", "danger"},
			},
			"mode": {
				Type: "string", Desc: "Durable action mode",
				Enum: []any{"ui_action", "capability_confirm", "server_action"},
			},
			"intent":   {Type: "string", Desc: "Public semantic intent"},
			"form_ref": {Type: "string", Desc: "Referenced form id"},
			"url":      {Type: "string", Desc: "Policy-controlled HTTPS URL"},
		},
		Required: []string{"kind", "id", "label", "mode", "intent"},
	}
}

func decodeAgentCardToolInput(raw string, target any) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	decoder := json.NewDecoder(io.LimitReader(
		bytes.NewBufferString(raw),
		128*1024+1,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("tool input must contain one JSON document")
	}
	return nil
}
