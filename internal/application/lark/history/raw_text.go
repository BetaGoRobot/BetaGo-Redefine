package history

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// OriginalText extracts persisted textual content before display substitutions.
// Unsupported media and malformed structured payloads are not evidence text.
func OriginalText(kind, raw string) (string, bool) {
	if !utf8.ValidString(raw) || strings.TrimSpace(raw) == "" {
		return "", false
	}
	switch kind {
	case "text":
		if strings.HasPrefix(strings.TrimSpace(raw), "{") {
			var msg struct {
				Text *string `json:"text"`
			}
			if json.Unmarshal([]byte(raw), &msg) != nil || msg.Text == nil || strings.TrimSpace(*msg.Text) == "" {
				return "", false
			}
			return *msg.Text, true
		}
		return raw, true
	case "post":
		type element struct {
			Tag  string `json:"tag"`
			Text string `json:"text"`
		}
		type post struct {
			Title   string      `json:"title"`
			Content [][]element `json:"content"`
		}
		var msg post
		if json.Unmarshal([]byte(raw), &msg) != nil {
			return "", false
		}
		if msg.Content == nil {
			var localized map[string]json.RawMessage
			if json.Unmarshal([]byte(raw), &localized) != nil {
				return "", false
			}
			found := false
			for _, lang := range []string{"zh_cn", "en_us", "ja_jp"} {
				if body, ok := localized[lang]; ok && json.Unmarshal(body, &msg) == nil && msg.Content != nil {
					found = true
					break
				}
			}
			if !found {
				return "", false
			}
		}
		lines := []string{}
		if msg.Title != "" {
			lines = append(lines, msg.Title)
		}
		for _, row := range msg.Content {
			var line strings.Builder
			for _, item := range row {
				if item.Tag == "text" {
					line.WriteString(item.Text)
				}
				if item.Tag == "at" {
					return "", false
				}
			}
			lines = append(lines, line.String())
		}
		text := strings.Join(lines, "\n")
		return text, strings.TrimSpace(text) != ""
	default:
		return "", false
	}
}
