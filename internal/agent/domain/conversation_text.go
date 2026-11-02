package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// This is a reading target, not a content limit. Indivisible Markdown/code
// units may exceed it; the whole plan still has transport resource ceilings.
const conversationReadingRunes = 180

// NormalizeConversationPlan preserves message order and reply targets while
// turning paragraphs into bounded logical sections. Sections append to one card
// or combine into one short message. Call before freezing delivery.
// It returns no partial plan if the full answer cannot fit the resource budget.
func NormalizeConversationPlan(p ConversationPlan) (ConversationPlan, error) {
	if err := p.Validate(); err != nil {
		return ConversationPlan{}, err
	}
	out := ConversationPlan{Choice: p.Choice, Summary: p.Summary, Messages: make([]ConversationMessage, 0, len(p.Messages))}
	for _, m := range p.Messages {
		for _, paragraph := range conversationParagraphs(m.Text) {
			for _, text := range splitConversationParagraph(paragraph) {
				out.Messages = append(out.Messages, ConversationMessage{Text: text, ReplyToMessageID: m.ReplyToMessageID})
				if len(out.Messages) > MaxConversationMessages {
					return ConversationPlan{}, ErrInvalid
				}
			}
		}
	}
	if err := out.Validate(); err != nil {
		return ConversationPlan{}, err
	}
	return out, nil
}

func conversationParagraphs(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); {
		if end := conversationProtectedEnd(text, i); end > i {
			i = end
			continue
		}
		if end := conversationParagraphBreak(text, i); end > i {
			if strings.TrimSpace(text[start:i]) != "" {
				out = append(out, text[start:i])
			}
			start, i = end, end
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
	if strings.TrimSpace(text[start:]) != "" {
		out = append(out, text[start:])
	}
	return out
}

func conversationNewlineEnd(s string, i int) int {
	if i < len(s) && s[i] == '\r' {
		i++
	}
	if i < len(s) && s[i] == '\n' {
		return i + 1
	}
	return 0
}

func conversationParagraphBreak(s string, i int) int {
	end := conversationNewlineEnd(s, i)
	if end == 0 {
		return 0
	}
	next := end
	for next < len(s) && (s[next] == ' ' || s[next] == '\t') {
		next++
	}
	end = conversationNewlineEnd(s, next)
	if end == 0 {
		return 0
	}
	for {
		next = end
		for next < len(s) && (s[next] == ' ' || s[next] == '\t') {
			next++
		}
		if after := conversationNewlineEnd(s, next); after > 0 {
			end = after
			continue
		}
		return end
	}
}

type conversationTextUnit struct {
	end, runes int
	boundary   int // space, clause, sentence, in increasing preference
}

func splitConversationParagraph(text string) []string {
	var units []conversationTextUnit
	for i := 0; i < len(text); {
		if end := conversationProtectedEnd(text, i); end > i {
			units = append(units, conversationTextUnit{end: end, runes: utf8.RuneCountInString(text[i:end])})
			i = end
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		boundary := 0
		switch {
		case strings.ContainsRune("。！？.!?", r):
			boundary = 3
		case strings.ContainsRune("，、；：,;:", r):
			boundary = 2
		case unicode.IsSpace(r):
			boundary = 1
		}
		i += size
		units = append(units, conversationTextUnit{end: i, runes: 1, boundary: boundary})
	}
	var out []string
	start, first := 0, 0
	for first < len(units) {
		last, runes := first, 0
		var boundaries [4]int
		for last < len(units) && runes+units[last].runes <= conversationReadingRunes {
			runes += units[last].runes
			if kind := units[last].boundary; kind > 0 && runes >= conversationReadingRunes/2 {
				boundaries[kind] = last + 1
			}
			last++
		}
		if last == first {
			last++ // An oversized protected unit is delivered intact.
		} else if last < len(units) {
			for kind := 3; kind > 0; kind-- {
				if boundaries[kind] > 0 {
					last = boundaries[kind]
					break
				}
			}
		}
		end := units[last-1].end
		// A whitespace-only tail belongs to the previous bubble, never a send.
		if strings.TrimSpace(text[start:end]) == "" && len(out) > 0 {
			out[len(out)-1] += text[start:end]
		} else {
			out = append(out, text[start:end])
		}
		start, first = end, last
	}
	return out
}

// Protected spans are indivisible even when longer than the reading target.
// Unclosed fences deliberately protect the remainder rather than damaging code.
func conversationProtectedEnd(s string, i int) int {
	if i == 0 || s[i-1] == '\n' {
		if end := conversationFenceEnd(s, i); end > i {
			return end
		}
	}
	if s[i] == '[' || (s[i] == '!' && i+1 < len(s) && s[i+1] == '[') {
		label := i
		if s[label] == '!' {
			label++
		}
		if end := conversationBalancedEnd(s, label, '[', ']'); end > 0 && end < len(s) && s[end] == '(' {
			if end = conversationLinkDestinationEnd(s, end); end > 0 {
				return end
			}
		}
	}
	if strings.HasPrefix(s[i:], "<https://") || strings.HasPrefix(s[i:], "<http://") {
		if close := strings.IndexByte(s[i:], '>'); close >= 0 {
			return i + close + 1
		}
	}
	if strings.HasPrefix(s[i:], "https://") || strings.HasPrefix(s[i:], "http://") {
		end := i
		for end < len(s) {
			r, size := utf8.DecodeRuneInString(s[end:])
			if unicode.IsSpace(r) || strings.ContainsRune("<>\"。！？；，、：“”‘’（）【】", r) {
				break
			}
			end += size
		}
		return end
	}
	if s[i] == '`' {
		end := i
		for end < len(s) && s[end] == '`' {
			end++
		}
		if close := strings.Index(s[end:], s[i:end]); close >= 0 {
			return end + close + end - i
		}
	}
	return 0
}

// Only an unescaped outer parenthesis closes an inline link. Parentheses in
// angle destinations and quoted titles are content, as are escaped delimiters.
func conversationLinkDestinationEnd(s string, start int) int {
	depth, angle := 1, false
	var quote byte
	for i := start + 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if quote != 0 {
			if s[i] == quote {
				quote = 0
			}
			continue
		}
		if angle {
			angle = s[i] != '>'
			continue
		}
		if depth == 1 {
			if s[i] == '<' {
				angle = true
				continue
			}
			previous, _ := utf8.DecodeLastRuneInString(s[:i])
			if (s[i] == '\'' || s[i] == '"') && unicode.IsSpace(previous) {
				quote = s[i]
				continue
			}
		}
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func conversationBalancedEnd(s string, i int, open, close byte) int {
	depth := 0
	for ; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == open {
			depth++
		} else if s[i] == close {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func conversationFenceEnd(s string, i int) int {
	marker := i
	for marker < len(s) && marker-i < 3 && s[marker] == ' ' {
		marker++
	}
	if marker >= len(s) || (s[marker] != '`' && s[marker] != '~') {
		return 0
	}
	end := marker
	for end < len(s) && s[end] == s[marker] {
		end++
	}
	width := end - marker
	if width < 3 {
		return 0
	}
	for line := end; line < len(s); {
		next := strings.IndexByte(s[line:], '\n')
		if next < 0 {
			break
		}
		line += next + 1
		close := line
		for close < len(s) && close-line < 3 && s[close] == ' ' {
			close++
		}
		finish := close
		for finish < len(s) && s[finish] == s[marker] {
			finish++
		}
		lineEnd := strings.IndexByte(s[finish:], '\n')
		if lineEnd < 0 {
			lineEnd = len(s)
		} else {
			lineEnd += finish
		}
		if finish-close >= width && strings.TrimSpace(s[finish:lineEnd]) == "" {
			if lineEnd > i && s[lineEnd-1] == '\r' {
				return lineEnd - 1
			}
			return lineEnd
		}
	}
	return len(s)
}
