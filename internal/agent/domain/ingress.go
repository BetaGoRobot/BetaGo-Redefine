package domain

import "encoding/json"

// IngressMetadata is produced by the verified transport, never by a model.
// Original authorship stays in SourceRef. Addressed is a participation hint;
// it does not grant approval or task ownership.
type IngressMetadata struct {
	Kind        string           `json:"kind"`
	SenderType  string           `json:"sender_type"`
	MessageType string           `json:"message_type,omitempty"`
	Addressed   bool             `json:"addressed"`
	Mentions    []IngressMention `json:"mentions,omitempty"`
	Control     *ControlAction   `json:"control,omitempty"`
	Provider    json.RawMessage  `json:"provider,omitempty"`
}
type IngressMention struct{ Key, OpenID, Name string }
type ControlAction struct {
	Action     string `json:"action"`
	TaskID     string `json:"task_id"`
	Revision   int64  `json:"revision"`
	ApprovalID string `json:"approval_id,omitempty"`
}
