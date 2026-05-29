package chatmetrics

import (
	"fmt"
	"strings"

	"github.com/VictoriaMetrics/metrics"
)

const labelMaxLen = 64

func RecordVictoriaMetrics(snapshot Snapshot) {
	chatID := sanitizeLabel(snapshot.Chat.ID)
	chatName := sanitizeLabel(snapshot.Chat.Name)
	chatStatus := sanitizeLabel(snapshot.Chat.Status)
	window := snapshot.RecentWindow.String()

	metrics.GetOrCreateGauge(
		fmt.Sprintf(`betago_lark_chat_info{chat_id=%q,chat_name=%q,chat_status=%q}`, chatID, chatName, chatStatus),
		nil,
	).Set(1)
	metrics.GetOrCreateGauge(
		fmt.Sprintf(`betago_lark_chat_member_count{chat_id=%q,chat_name=%q}`, chatID, chatName),
		nil,
	).Set(float64(snapshot.MemberCount))
	metrics.GetOrCreateGauge(
		fmt.Sprintf(`betago_lark_chat_recent_message_count{chat_id=%q,chat_name=%q,window=%q}`, chatID, chatName, window),
		nil,
	).Set(float64(snapshot.RecentMessageCount))
}

func sanitizeLabel(v string) string {
	runes := []rune(strings.ToValidUTF8(v, "?"))
	if len(runes) <= labelMaxLen {
		return string(runes)
	}
	return string(runes[:labelMaxLen]) + "..."
}
