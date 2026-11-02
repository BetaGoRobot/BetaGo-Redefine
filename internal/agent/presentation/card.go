// Package presentation composes bounded display-only Feishu schema 2 cards.
// It has no transport, credentials, callbacks, actor authority or workflow state.
package presentation

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/web"
)

type Bar struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}
type Section struct {
	Kind    string     `json:"kind"`
	Heading string     `json:"heading,omitempty"`
	Text    string     `json:"text,omitempty"`
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	Bars    []Bar      `json:"bars,omitempty"`
}
type Card struct {
	Title    string    `json:"title"`
	Theme    string    `json:"theme,omitempty"`
	Sections []Section `json:"sections"`
	Links    []Link    `json:"links,omitempty"`
}

func safeText(s string, max int, empty bool) bool {
	return utf8.ValidString(s) && len(s) <= max && (empty || strings.TrimSpace(s) != "") && !strings.ContainsAny(s, "<\x00") && !strings.Contains(s, "](") && !strings.Contains(s, "]:") && !strings.Contains(s, "http://") && !strings.Contains(s, "https://")
}
func validLink(l Link) bool {
	if !safeText(l.Label, 128, false) || strings.ContainsAny(l.Label, "[]\n\r") || !web.PublicURL(l.URL) {
		return false
	}
	u, err := url.Parse(l.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" && strings.ContainsAny(u.Fragment, "\r\n") {
		return false
	}
	for key := range u.Query() {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
		switch normalized {
		case "token", "accesstoken", "apikey", "password", "secret", "authorization", "credential", "session":
			return false
		}
	}
	return !strings.ContainsAny(l.URL, "()<>")
}
func markdown(s string) map[string]any { return map[string]any{"tag": "markdown", "content": s} }

// Compose emits only the officially supported schema/body/header/markdown
// structure. Tables use labeled rows and bar charts use scaled text bars, so
// every value stays readable on clients without native chart/table support.
func Compose(c Card) ([]byte, error) {
	if !safeText(c.Title, 128, false) || len(c.Sections) < 1 || len(c.Sections) > 12 || len(c.Links) > 8 {
		return nil, domain.ErrInvalid
	}
	theme := c.Theme
	if theme == "" {
		theme = "blue"
	}
	switch theme {
	case "blue", "green", "orange", "red", "grey":
	default:
		return nil, domain.ErrInvalid
	}
	elements := []any{}
	for _, s := range c.Sections {
		if !safeText(s.Heading, 128, true) {
			return nil, domain.ErrInvalid
		}
		var b strings.Builder
		if s.Heading != "" {
			fmt.Fprintf(&b, "**%s**\n", s.Heading)
		}
		switch s.Kind {
		case "markdown":
			if !safeText(s.Text, 8192, false) || len(s.Columns) > 0 || len(s.Rows) > 0 || len(s.Bars) > 0 {
				return nil, domain.ErrInvalid
			}
			b.WriteString(s.Text)
		case "table":
			if s.Text != "" || len(s.Bars) > 0 || len(s.Columns) < 1 || len(s.Columns) > 6 || len(s.Rows) < 1 || len(s.Rows) > 20 {
				return nil, domain.ErrInvalid
			}
			for _, col := range s.Columns {
				if !safeText(col, 64, false) {
					return nil, domain.ErrInvalid
				}
			}
			for rowIndex, row := range s.Rows {
				if len(row) != len(s.Columns) {
					return nil, domain.ErrInvalid
				}
				if rowIndex > 0 {
					b.WriteString("\n")
				}
				for i, value := range row {
					if !safeText(value, 256, true) {
						return nil, domain.ErrInvalid
					}
					fmt.Fprintf(&b, "%s: %s", s.Columns[i], value)
					if i < len(row)-1 {
						b.WriteString(" · ")
					}
				}
			}
		case "bar_chart":
			if s.Text != "" || len(s.Columns) > 0 || len(s.Rows) > 0 || len(s.Bars) < 1 || len(s.Bars) > 12 {
				return nil, domain.ErrInvalid
			}
			max := 0.0
			for _, bar := range s.Bars {
				if !safeText(bar.Label, 64, false) || math.IsNaN(bar.Value) || math.IsInf(bar.Value, 0) || bar.Value < 0 || bar.Value > 1e15 {
					return nil, domain.ErrInvalid
				}
				if bar.Value > max {
					max = bar.Value
				}
			}
			for i, bar := range s.Bars {
				width := 0
				if max > 0 {
					width = int(math.Round(bar.Value / max * 16))
				}
				if bar.Value > 0 && width == 0 {
					width = 1
				}
				fmt.Fprintf(&b, "%s  %s  %g", bar.Label, strings.Repeat("█", width), bar.Value)
				if i < len(s.Bars)-1 {
					b.WriteString("\n")
				}
			}
		default:
			return nil, domain.ErrInvalid
		}
		elements = append(elements, markdown(b.String()))
	}
	if len(c.Links) > 0 {
		var b strings.Builder
		for i, link := range c.Links {
			if !validLink(link) {
				return nil, domain.ErrInvalid
			}
			if i > 0 {
				b.WriteString(" · ")
			}
			fmt.Fprintf(&b, "[%s](%s)", link.Label, link.URL)
		}
		elements = append(elements, markdown(b.String()))
	}
	payload := map[string]any{"schema": "2.0", "config": map[string]any{"update_multi": true}, "header": map[string]any{"title": map[string]any{"tag": "plain_text", "content": c.Title}, "template": theme}, "body": map[string]any{"elements": elements}}
	data, err := json.Marshal(payload)
	if err != nil || len(data) > 24<<10 {
		return nil, domain.ErrInvalid
	}
	return data, nil
}
