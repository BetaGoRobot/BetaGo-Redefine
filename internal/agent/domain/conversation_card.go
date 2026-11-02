package domain

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const ConversationCardRunes = 280

type ConversationFrame struct{ MessageType, Content, Text string }

// ConversationFrames renders the complete immutable answer before transport.
// Each cumulative card frame must fit; no partial frames survive an error.
func ConversationFrames(p ConversationPlan) ([]ConversationFrame, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Choice == "observe" {
		return nil, nil
	}
	sections := make([]string, len(p.Messages))
	for i, m := range p.Messages {
		sections[i] = m.Text
	}
	summary := p.Summary
	if strings.TrimSpace(summary) == "" {
		summary = ""
	}
	text := conversationVisibleText(summary, sections)
	if p.Choice != "expand" && utf8.RuneCountInString(text) <= ConversationCardRunes {
		b, _ := json.Marshal(map[string]string{"text": text})
		if len(b) > MaxConversationBytes {
			return nil, ErrInvalid
		}
		return []ConversationFrame{{"text", string(b), text}}, nil
	}
	if summary == "" {
		// Preview only the first frozen section; later sections are not yet shown.
		preview := []rune(sections[0])
		if len(preview) > 120 {
			preview = preview[:120]
			summary = string(preview) + "…"
		} else {
			summary = string(preview)
		}
	}
	frames := make([]ConversationFrame, 0, len(sections))
	elements := make([]any, 0, len(sections))
	for i, section := range sections {
		elements = append(elements, map[string]any{"tag": "markdown", "content": section})
		card := map[string]any{"schema": "2.0", "config": map[string]any{"update_multi": true}, "body": map[string]any{"elements": []any{
			map[string]any{"tag": "markdown", "content": summary},
			map[string]any{"tag": "collapsible_panel", "expanded": false, "header": map[string]any{"title": map[string]any{"tag": "markdown", "content": "详情"}}, "elements": elements},
		}}}
		b, err := json.Marshal(card)
		if err != nil || len(b) > MaxConversationBytes {
			return nil, ErrInvalid
		}
		frames = append(frames, ConversationFrame{"interactive", string(b), conversationVisibleText(summary, sections[:i+1])})
	}
	return frames, nil
}

// Preserve unique visible summary content in both transport and history. Only
// exact equality with the full detail or an entire section permits deduping it;
// substring overlap is not evidence that the summary's meaning is redundant.
func conversationVisibleText(summary string, sections []string) string {
	text := strings.Join(sections, "\n\n")
	if summary == "" || summary == text {
		return text
	}
	for _, section := range sections {
		if summary == section {
			return text
		}
	}
	return summary + "\n\n" + text
}

// ConversationDeliveryText excludes approval cards and unknown interactive
// content. Only a backend-pinned ordinary card has conversational provenance.
func ConversationDeliveryText(d Delivery) (string, bool) {
	if d.Kind != "final" {
		return "", false
	}
	switch d.MessageType {
	case "text":
		var content struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(d.Content), &content) != nil || strings.TrimSpace(content.Text) == "" {
			return "", false
		}
		return content.Text, true
	case "interactive":
		if strings.TrimSpace(d.ConversationText) != "" && utf8.ValidString(d.ConversationText) {
			return d.ConversationText, true
		}
	}
	return "", false
}
