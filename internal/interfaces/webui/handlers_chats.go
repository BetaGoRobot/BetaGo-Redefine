package webui

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chatmetrics"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
)

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	if s.chats == nil {
		writeError(w, http.StatusServiceUnavailable, "chat service unavailable")
		return
	}
	chats, err := s.chats.ListChats(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": chats,
		"total": len(chats),
	})
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.chats == nil {
		writeError(w, http.StatusServiceUnavailable, "chat service unavailable")
		return
	}
	detail, err := s.chats.GetChat(r.Context(), chatID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// larkChatService 是基于 Lark OpenAPI 的默认 ChatService 实现。
//
// 群列表复用 chatmetrics.ListChats（含 avatar 字段），详情复用
// larkchat.GetChatInfoCache + chatmetrics.CountChatMembers。
type larkChatService struct{}

// NewLarkChatService 返回基于 Lark OpenAPI 的群信息服务。
func NewLarkChatService() ChatService { return &larkChatService{} }

func (l *larkChatService) ListChats(ctx context.Context) ([]ChatSummary, error) {
	summaries := make([]ChatSummary, 0)
	for item := range chatmetrics.ListChats(ctx) {
		if item == nil {
			continue
		}
		summaries = append(summaries, ChatSummary{
			ChatID:      strings.TrimSpace(ptr(item.ChatId)),
			Name:        strings.TrimSpace(ptr(item.Name)),
			Avatar:      strings.TrimSpace(ptr(item.Avatar)),
			Description: strings.TrimSpace(ptr(item.Description)),
			ChatStatus:  strings.TrimSpace(ptr(item.ChatStatus)),
			External:    item.External != nil && *item.External,
			Tenant:      strings.TrimSpace(ptr(item.TenantKey)),
		})
	}
	return summaries, nil
}

func (l *larkChatService) GetChat(ctx context.Context, chatID string) (*ChatDetail, error) {
	data, err := larkchat.GetChatInfoCache(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("chat not found")
	}
	detail := &ChatDetail{
		ChatSummary: ChatSummary{
			ChatID:      chatID,
			Name:        strings.TrimSpace(ptr(data.Name)),
			Avatar:      strings.TrimSpace(ptr(data.Avatar)),
			Description: strings.TrimSpace(ptr(data.Description)),
			ChatStatus:  strings.TrimSpace(ptr(data.ChatStatus)),
			External:    data.External != nil && *data.External,
		},
		OwnerID:  strings.TrimSpace(ptr(data.OwnerId)),
		ChatMode: strings.TrimSpace(ptr(data.ChatMode)),
	}
	if count, err := chatmetrics.CountChatMembers(ctx, chatID); err == nil {
		detail.MemberCount = count
	}
	return detail, nil
}

func ptr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
