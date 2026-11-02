package historyimport

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	event "github.com/larksuite/oapi-sdk-go/v3/event"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func sameFacts(a, b domain.Message) bool {
	a.EventID, b.EventID = "", ""
	a.Raw, b.Raw = nil, nil
	a.Source.OccurredAt = a.Source.OccurredAt.UTC()
	b.Source.OccurredAt = b.Source.OccurredAt.UTC()
	a.UpdatedAt = a.UpdatedAt.UTC()
	b.UpdatedAt = b.UpdatedAt.UTC()
	if len(a.Media) == 0 {
		a.Media = nil
	}
	if len(b.Media) == 0 {
		b.Media = nil
	}
	return reflect.DeepEqual(a, b)
}
func ptr[T any](v T) *T { return &v }
func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// Re-normalize the recorded provider response with the existing transport
// parser. This performs no API call and prevents edited ledger facts from
// disagreeing with their author, timestamps, text, links or media provenance.
func validateRefreshProvider(m domain.Message, meta domain.IngressMetadata) error {
	var p im.Message
	if len(meta.Provider) == 0 || json.Unmarshal(meta.Provider, &p) != nil || p.Sender == nil || p.Body == nil || str(p.Sender.IdType) != "open_id" || str(p.Sender.SenderType) != "user" || (p.Deleted != nil && *p.Deleted) || meta.Addressed || meta.MessageType != str(p.MsgType) {
		return fmt.Errorf("%w: canonical provider human identity", domain.ErrInvalid)
	}
	normalizer, err := lark.NewNormalizer(lark.NormalizeConfig{AppID: m.Source.Scope.AppID, BotOpenID: "history-import-validation", TenantID: m.Source.Scope.TenantID, AllowedChatIDs: []string{m.Source.Scope.ChatID}})
	if err != nil {
		return err
	}
	input := &im.P2MessageReceiveV1{EventV2Base: &event.EventV2Base{Header: &event.EventHeader{AppID: m.Source.Scope.AppID, TenantKey: m.Source.Scope.TenantID, EventID: m.EventID, CreateTime: strconv.FormatInt(m.UpdatedAt.UnixMilli(), 10)}}, Event: &im.P2MessageReceiveV1Data{Sender: &im.EventSender{SenderId: &im.UserId{OpenId: p.Sender.Id}, SenderType: p.Sender.SenderType, TenantKey: p.Sender.TenantKey}, Message: &im.EventMessage{MessageId: p.MessageId, ChatId: p.ChatId, ChatType: ptr("group"), CreateTime: p.CreateTime, UpdateTime: p.UpdateTime, MessageType: p.MsgType, Content: p.Body.Content, ParentId: p.ParentId, RootId: p.RootId, ThreadId: p.ThreadId}}}
	normalized, err := normalizer.Message(input)
	if err != nil || !sameFacts(m, normalized.Message) {
		return fmt.Errorf("%w: canonical facts disagree with recorded provider", domain.ErrInvalid)
	}
	return nil
}
