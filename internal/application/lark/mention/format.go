package mention

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func NormalizeOutgoingText(ctx context.Context, chatID, text string) (string, error) {
	if strings.TrimSpace(chatID) == "" || text == "" || !strings.Contains(text, "@") {
		return text, nil
	}

	memberMap, err := larkuser.GetUserMapFromChatIDCache(ctx, chatID)
	if err != nil {
		return text, err
	}
	return normalizeTextWithMentions(text, nil, memberMap), nil
}

func NormalizeReplyText(ctx context.Context, chatID string, messageList history.OpensearchMsgLogList, text string) (string, error) {
	if strings.TrimSpace(chatID) == "" || text == "" || !strings.Contains(text, "@") {
		return text, nil
	}

	memberMap, err := larkuser.GetUserMapFromChatIDCache(ctx, chatID)
	if err != nil {
		return text, err
	}
	return normalizeTextWithMentions(text, messageList, memberMap), nil
}

func normalizeTextWithMentions(text string, messageList history.OpensearchMsgLogList, memberMap map[string]*larkim.ListMember) string {
	if text == "" {
		return text
	}

	replacements := make(map[string]string, len(messageList)*4+len(memberMap)*2)
	for _, item := range messageList {
		if item == nil {
			continue
		}
		addReplacement(replacements, strings.TrimSpace(item.UserName), strings.TrimSpace(item.OpenID), strings.TrimSpace(item.UserName))
		for _, mention := range item.MentionList {
			if mention == nil || mention.Id == nil || mention.Name == nil {
				continue
			}
			addReplacement(replacements, strings.TrimSpace(*mention.Name), strings.TrimSpace(*mention.Id), strings.TrimSpace(*mention.Name))
		}
	}
	for _, member := range memberMap {
		if member == nil || member.MemberId == nil || member.Name == nil {
			continue
		}
		addReplacement(replacements, strings.TrimSpace(*member.Name), strings.TrimSpace(*member.MemberId), strings.TrimSpace(*member.Name))
	}
	if len(replacements) == 0 {
		return text
	}
	return utils.BuildTrie(replacements).ReplaceMentionsWithTrie(text)
}

func addReplacement(replacements map[string]string, name, openID, displayName string) {
	if openID == "" || displayName == "" {
		return
	}
	at := larkmsg.AtUser(openID, displayName)
	if name != "" {
		replacements[name] = at
	}
	replacements[openID] = at
}
