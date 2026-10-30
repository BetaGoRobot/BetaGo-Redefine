package engine

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

// Automatic background context describes the current episode, rather than the
// last 32 messages regardless of whether the group was idle for several days.
// Explicit reply ancestry and task-bound materials remain available separately.
const recentConversationWindow = 30 * time.Minute

// A short implicit continuation may refer to the last answer after an idle
// interval. Restore one confirmed predecessor, never the whole old episode.
const implicitReplyLookback = 24 * time.Hour

func recentContextWindow(anchor time.Time, current domain.Message, recent []domain.Message, replies []pgstore.ConversationReply) ([]domain.Message, []pgstore.ConversationReply) {
	cutoff := anchor.Add(-recentConversationWindow)
	keep := func(id string, at time.Time) bool {
		if anchor.IsZero() || at.IsZero() || at.After(anchor) {
			return false
		}
		return !at.Before(cutoff) || id != "" && (id == current.Source.MessageID || id == current.ParentID || id == current.RootID)
	}
	selected := make([]domain.Message, 0, len(recent))
	for _, message := range recent {
		if keep(message.Source.MessageID, message.Source.OccurredAt) {
			selected = append(selected, message)
		}
	}
	confirmed := make([]pgstore.ConversationReply, 0, len(replies))
	for _, reply := range replies {
		if keep(reply.MessageID, reply.OccurredAt) {
			confirmed = append(confirmed, reply)
		}
	}
	if len(confirmed) == 0 && current.ParentID == "" && current.RootID == "" && implicitReplyContinuation(current.Text) && !hasOtherRecentHuman(current.Source.MessageID, selected) {
		var predecessor *pgstore.ConversationReply
		for i := range replies {
			reply := &replies[i]
			if reply.MessageID == "" || strings.TrimSpace(reply.Text) == "" || reply.OccurredAt.IsZero() || reply.OccurredAt.After(anchor) || reply.OccurredAt.Before(anchor.Add(-implicitReplyLookback)) {
				continue
			}
			if !current.Source.OccurredAt.IsZero() && reply.OccurredAt.After(current.Source.OccurredAt) {
				continue
			}
			if predecessor == nil || reply.OccurredAt.After(predecessor.OccurredAt) || reply.OccurredAt.Equal(predecessor.OccurredAt) && reply.MessageID < predecessor.MessageID {
				predecessor = reply
			}
		}
		if predecessor != nil {
			confirmed = append(confirmed, *predecessor)
		}
	}
	return selected, confirmed
}

func hasOtherRecentHuman(currentID string, messages []domain.Message) bool {
	for _, message := range messages {
		if message.Source.MessageID == currentID || strings.HasPrefix(message.Source.MessageID, "card:") {
			continue
		}
		var meta domain.IngressMetadata
		// Unknown metadata is conservatively treated as active human context.
		// A short question amid a new episode must not revive yesterday's bot.
		if len(message.Raw) == 0 || json.Unmarshal(message.Raw, &meta) != nil || meta.SenderType != "bot" && meta.SenderType != "app" {
			return true
		}
	}
	return false
}

func implicitReplyContinuation(text string) bool {
	// Match entire referential fragments. Mere shortness, a question mark, or
	// a new entity followed by "呢" must not resurrect unrelated old replies.
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return r
	}, text)
	for _, prefix := range []string{"", "那", "所以"} {
		candidate := strings.TrimPrefix(text, prefix)
		switch candidate {
		case "你觉得", "你觉得呢", "你怎么看", "你怎么看呢", "然后呢", "这个呢", "那个呢", "刚才那个", "刚才那个呢", "刚才这个", "刚才这个呢", "刚刚那个", "刚刚那个呢", "刚刚这个", "刚刚这个呢", "继续说", "继续呢", "接着说", "接着呢":
			return true
		}
	}
	return false
}
