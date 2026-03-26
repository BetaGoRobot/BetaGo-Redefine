package message

import (
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Envelope carries runtime message handling state.
type Envelope struct {
	ChatID      string `json:"chat_id,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
	ChatType    string `json:"chat_type,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	ThreadID    string `json:"thread_id,omitempty"`
	ActorOpenID string `json:"actor_open_id,omitempty"`
}

// EventMessage implements runtime message handling behavior.
func EventMessage(event *larkim.P2MessageReceiveV1) *larkim.EventMessage {
	if event == nil || event.Event == nil {
		return nil
	}
	return event.Event.Message
}

// ChatID implements runtime message handling behavior.
func ChatID(event *larkim.P2MessageReceiveV1) string {
	msg := EventMessage(event)
	if msg == nil || msg.ChatId == nil {
		return ""
	}
	return strings.TrimSpace(*msg.ChatId)
}

// MessageID implements runtime message handling behavior.
func MessageID(event *larkim.P2MessageReceiveV1) string {
	msg := EventMessage(event)
	if msg == nil || msg.MessageId == nil {
		return ""
	}
	return strings.TrimSpace(*msg.MessageId)
}

// ChatType implements runtime message handling behavior.
func ChatType(event *larkim.P2MessageReceiveV1) string {
	msg := EventMessage(event)
	if msg == nil || msg.ChatType == nil {
		return ""
	}
	return strings.TrimSpace(*msg.ChatType)
}

// ParentID implements runtime message handling behavior.
func ParentID(event *larkim.P2MessageReceiveV1) string {
	msg := EventMessage(event)
	if msg == nil || msg.ParentId == nil {
		return ""
	}
	return strings.TrimSpace(*msg.ParentId)
}

// ThreadID implements runtime message handling behavior.
func ThreadID(event *larkim.P2MessageReceiveV1) string {
	msg := EventMessage(event)
	if msg == nil || msg.ThreadId == nil {
		return ""
	}
	return strings.TrimSpace(*msg.ThreadId)
}

// ActorOpenID implements runtime message handling behavior.
func ActorOpenID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil || event.Event.Sender.SenderId.OpenId == nil {
		return ""
	}
	return strings.TrimSpace(*event.Event.Sender.SenderId.OpenId)
}

// CaptureEnvelope implements runtime message handling behavior.
func CaptureEnvelope(event *larkim.P2MessageReceiveV1) Envelope {
	return Envelope{
		ChatID:      ChatID(event),
		MessageID:   MessageID(event),
		ChatType:    ChatType(event),
		ParentID:    ParentID(event),
		ThreadID:    ThreadID(event),
		ActorOpenID: ActorOpenID(event),
	}
}

// BuildMessageReceiveEvent implements runtime message handling behavior.
func BuildMessageReceiveEvent(envelope Envelope) *larkim.P2MessageReceiveV1 {
	envelope.ChatID = strings.TrimSpace(envelope.ChatID)
	envelope.MessageID = strings.TrimSpace(envelope.MessageID)
	envelope.ChatType = strings.TrimSpace(envelope.ChatType)
	envelope.ParentID = strings.TrimSpace(envelope.ParentID)
	envelope.ThreadID = strings.TrimSpace(envelope.ThreadID)
	envelope.ActorOpenID = strings.TrimSpace(envelope.ActorOpenID)

	msg := &larkim.EventMessage{
		ChatId:    &envelope.ChatID,
		MessageId: &envelope.MessageID,
		ChatType:  &envelope.ChatType,
	}
	if envelope.ParentID != "" {
		msg.ParentId = &envelope.ParentID
	}
	if envelope.ThreadID != "" {
		msg.ThreadId = &envelope.ThreadID
	}

	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: msg,
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &envelope.ActorOpenID,
				},
			},
		},
	}
}

// CloneWithActorOpenID implements runtime message handling behavior.
func CloneWithActorOpenID(event *larkim.P2MessageReceiveV1, actorOpenID string) *larkim.P2MessageReceiveV1 {
	actorOpenID = strings.TrimSpace(actorOpenID)
	if event == nil || actorOpenID == "" {
		return event
	}
	if ActorOpenID(event) != "" {
		return event
	}

	cloned := *event
	if event.Event == nil {
		cloned.Event = &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: &actorOpenID},
			},
		}
		return &cloned
	}

	eventData := *event.Event
	cloned.Event = &eventData
	if event.Event.Sender == nil {
		cloned.Event.Sender = &larkim.EventSender{
			SenderId: &larkim.UserId{OpenId: &actorOpenID},
		}
		return &cloned
	}

	sender := *event.Event.Sender
	cloned.Event.Sender = &sender
	if event.Event.Sender.SenderId == nil {
		cloned.Event.Sender.SenderId = &larkim.UserId{OpenId: &actorOpenID}
		return &cloned
	}

	senderID := *event.Event.Sender.SenderId
	senderID.OpenId = &actorOpenID
	cloned.Event.Sender.SenderId = &senderID
	return &cloned
}

// ParseContentStruct implements runtime message handling behavior.
func ParseContentStruct(raw string) ark_dal.ContentStruct {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ark_dal.ContentStruct{}
	}

	content := ark_dal.ContentStruct{}
	if err := sonic.UnmarshalString(raw, &content); err == nil {
		return content
	}

	repaired, repairErr := jsonrepair.RepairJSON(raw)
	if repairErr == nil {
		if err := sonic.UnmarshalString(repaired, &content); err == nil {
			return content
		}
	}

	return ark_dal.ContentStruct{Reply: raw}
}
