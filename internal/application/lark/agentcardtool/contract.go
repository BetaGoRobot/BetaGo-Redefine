package agentcardtool

import (
	"context"
	"encoding/json"
)

type DiscoverRequest struct {
	Version  string `json:"version,omitempty"`
	Category string `json:"category,omitempty"`
	Name     string `json:"name,omitempty"`
}

type Component struct {
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	Version       string   `json:"version"`
	Purpose       string   `json:"purpose"`
	Fields        []string `json:"fields"`
	BudgetCost    int      `json:"budget_cost"`
	SafeExample   string   `json:"safe_example"`
	DisallowedUse string   `json:"disallowed_use"`
	Lifecycles    []string `json:"lifecycles"`
	ActionModes   []string `json:"action_modes,omitempty"`
}

type DiscoverResponse struct {
	Version    string      `json:"version"`
	Components []Component `json:"components"`
}

type Card struct {
	Version string         `json:"version,omitempty"`
	Title   string         `json:"title"`
	Theme   string         `json:"theme,omitempty"`
	Blocks  []Block        `json:"blocks"`
	Actions []Action       `json:"actions,omitempty"`
	Meta    PublicCardMeta `json:"meta,omitempty"`
}

type PublicCardMeta struct {
	Purpose string   `json:"purpose,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Labels  []string `json:"labels,omitempty"`
	Locale  string   `json:"locale,omitempty"`
}

type Block struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`

	Text    string   `json:"text,omitempty"`
	Title   string   `json:"title,omitempty"`
	Items   []Fact   `json:"items,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Blocks  []Block  `json:"blocks,omitempty"`

	FieldID     string   `json:"field_id,omitempty"`
	FormID      string   `json:"form_id,omitempty"`
	Label       string   `json:"label,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Purpose     string   `json:"purpose,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Multiline   bool     `json:"multiline,omitempty"`
	MinLength   int      `json:"min_length,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
	Options     []Option `json:"options,omitempty"`
}

type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Column struct {
	ID     string  `json:"id"`
	Width  int     `json:"width,omitempty"`
	Blocks []Block `json:"blocks"`
}

type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Action struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Label   string `json:"label"`
	Style   string `json:"style,omitempty"`
	Mode    string `json:"mode"`
	Intent  string `json:"intent"`
	FormRef string `json:"form_ref,omitempty"`
	URL     string `json:"url,omitempty"`
}

type Interaction struct {
	Mode             string `json:"mode"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
}

type ComposeRequest struct {
	Purpose     string      `json:"purpose"`
	Card        Card        `json:"card"`
	Interaction Interaction `json:"interaction"`
}

type ComposeContext struct {
	ChatID           string
	ActorOpenID      string
	ReplyToMessageID string
	TriggerEventID   string
}

type Issue struct {
	Code   string `json:"code"`
	Path   string `json:"path"`
	Limit  int    `json:"limit,omitempty"`
	Actual int    `json:"actual,omitempty"`
}

type ComposeResponse struct {
	Status        string  `json:"status"`
	Attempt       int     `json:"attempt,omitempty"`
	Issues        []Issue `json:"issues,omitempty"`
	Fallback      string  `json:"fallback,omitempty"`
	CardRef       string  `json:"card_ref,omitempty"`
	MessageID     string  `json:"message_id,omitempty"`
	InteractionID string  `json:"interaction_id,omitempty"`
	Revision      int64   `json:"revision,omitempty"`
}

type Service interface {
	DiscoverComponents(context.Context, DiscoverRequest) (DiscoverResponse, error)
	ComposeCard(
		context.Context,
		ComposeContext,
		ComposeRequest,
	) (ComposeResponse, error)
}

func MarshalResponse(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `{"status":"failed"}`
	}
	return string(encoded)
}
