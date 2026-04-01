package tui

import (
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay"
	"github.com/charmbracelet/lipgloss"
)

const (
	rootPanelWidth      = 84
	sectionPanelWidth   = 76
	statCardWidth       = 24
	maxVisibleChatItems = 8
)

var (
	asciiBorder = lipgloss.Border{
		Top:          "-",
		Bottom:       "-",
		Left:         "|",
		Right:        "|",
		TopLeft:      "+",
		TopRight:     "+",
		BottomLeft:   "+",
		BottomRight:  "+",
		MiddleLeft:   "+",
		MiddleRight:  "+",
		Middle:       "+",
		MiddleTop:    "+",
		MiddleBottom: "+",
	}

	rootPanelStyle = lipgloss.NewStyle().
			Width(rootPanelWidth).
			BorderStyle(asciiBorder).
			BorderForeground(lipgloss.Color("63")).
			Padding(1, 2)

	stepBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("63")).
			Padding(0, 1)

	statusBadgeStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("29")).
				Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230"))

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	sectionStyle = lipgloss.NewStyle().
			Width(sectionPanelWidth).
			BorderStyle(asciiBorder).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("229"))

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229"))

	inputBoxStyle = lipgloss.NewStyle().
			Width(sectionPanelWidth-4).
			BorderStyle(asciiBorder).
			BorderForeground(lipgloss.Color("69")).
			Padding(0, 1)

	inputValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230"))

	inputPlaceholderStyle = lipgloss.NewStyle().
				Italic(true).
				Foreground(lipgloss.Color("244"))

	statCardStyle = lipgloss.NewStyle().
			Width(statCardWidth).
			BorderStyle(asciiBorder).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	statValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("80"))

	listRowStyle = lipgloss.NewStyle().
			Width(sectionPanelWidth-6).
			Padding(0, 1)

	selectedRowStyle = lipgloss.NewStyle().
				Width(sectionPanelWidth-6).
				Padding(0, 1).
				Bold(true).
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("63"))

	errorStyle = lipgloss.NewStyle().
			Width(sectionPanelWidth).
			BorderStyle(asciiBorder).
			BorderForeground(lipgloss.Color("160")).
			Foreground(lipgloss.Color("224")).
			Padding(0, 1)
)

func (m Model) View() string {
	switch m.state.screen {
	case ScreenFilterBuilder:
		return renderSimpleScreen(
			"Step 2/5",
			"Filter Builder",
			"Sampling window is still driven by CLI flags in this iteration.",
			"",
			renderStatsRow(
				renderStatCard("Chat", selectedChatName(m.state.selectedChat)),
				renderStatCard("Days", fmt.Sprintf("%d", m.config.Days)),
				renderStatCard("Limit", fmt.Sprintf("%d", m.config.Limit)),
			),
			renderSection("Next Action", "Press Enter to load candidate samples for the selected chat."),
		)
	case ScreenSamplePreview:
		return renderSimpleScreen(
			"Step 3/5",
			"Sample Preview",
			"Review candidate messages before replay.",
			"",
			renderStatsRow(
				renderStatCard("Samples", fmt.Sprintf("%d", len(m.state.samples))),
				renderStatCard("Selected", fmt.Sprintf("%d", len(m.state.selectedSamples))),
				renderStatCard("Cursor", fmt.Sprintf("%d", m.state.sampleCursor+1)),
			),
			renderSection("Controls", "Up/Down move | Space toggles current sample | a selects all | Enter starts replay."),
		)
	case ScreenReplayRunner:
		return renderSimpleScreen(
			"Step 4/5",
			"Replay Runner",
			"Batch replay runs in read-only mode and writes a local report.",
			"",
			renderStatsRow(
				renderStatCard("Completed", fmt.Sprintf("%d / %d", m.state.progress.Completed, m.state.progress.Total)),
				renderStatCard("Success", fmt.Sprintf("%d", m.state.progress.Success)),
				renderStatCard("Failed", fmt.Sprintf("%d", m.state.progress.Failed)),
			),
			renderSection("Progress", m.state.progressBar.ViewAs(batchPercent(m.state.progress.Completed, m.state.progress.Total))),
		)
	case ScreenReportViewer:
		return renderReportView(m)
	default:
		return renderChatPickerView(m)
	}
}

func renderChatPickerView(m Model) string {
	header := renderScreenHeader(
		"Step 1/5",
		"Chat Picker",
		"Local Catalog",
		"Pick a chat by chat_name. Catalog is loaded once on startup and filtering is local full-text.",
	)

	stats := renderStatsRow(
		renderStatCard("Catalog", fmt.Sprintf("%d chats", len(m.state.allChatCandidates))),
		renderStatCard("Matches", fmt.Sprintf("%d shown", len(m.state.chatCandidates))),
		renderStatCard("Window", fmt.Sprintf("%d days", m.config.Days)),
	)

	searchBody := []string{
		labelStyle.Render("Search Chat Name / ID"),
		mutedStyle.Render("Type any word from chat_name or chat_id. Empty query keeps recent active chats visible."),
		renderSearchInput(m.state.searchQuery),
	}

	resultBody := []string{
		labelStyle.Render(renderVisibleCountLine(len(m.state.chatCandidates), m.state.chatCursor)),
		renderChatCandidates(m.state.chatCandidates, m.state.chatCursor),
	}

	helpBody := []string{
		helpStyle.Render("Enter chooses the highlighted chat."),
		helpStyle.Render("Up/Down moves within the result list."),
		helpStyle.Render("q or Ctrl+C exits the replay TUI."),
	}

	sections := []string{
		header,
		stats,
		renderSection("Search", strings.Join(searchBody, "\n")),
		renderSection("Matches", strings.Join(resultBody, "\n")),
		renderSection("Controls", strings.Join(helpBody, "\n")),
	}
	if strings.TrimSpace(m.state.errText) != "" {
		sections = append(sections, errorStyle.Render("Error\n"+strings.TrimSpace(m.state.errText)))
	}
	return rootPanelStyle.Render(strings.Join(sections, "\n\n"))
}

func renderReportView(m Model) string {
	if m.state.batchResult == nil {
		return renderSimpleScreen(
			"Step 5/5",
			"Report Viewer",
			"Batch result is not available yet.",
			"",
			renderSection("Status", "No report loaded."),
		)
	}
	if m.state.reportView == ReportViewCaseDetail {
		return renderSimpleScreen(
			"Step 5/5",
			"Case Detail",
			"Inspect a single replay case.",
			"",
			renderSection("Case", fmt.Sprintf("message_id: %s", m.state.activeCaseMessageID)),
			renderSection("Controls", "Press b or Esc to return to the summary list."),
		)
	}
	return renderSimpleScreen(
		"Step 5/5",
		"Report Summary",
		"Replay outcomes across the selected batch.",
		"",
		renderStatsRow(
			renderStatCard("Success", fmt.Sprintf("%d", m.state.batchResult.Summary.SuccessCount)),
			renderStatCard("Partial", fmt.Sprintf("%d", m.state.batchResult.Summary.PartialCount)),
			renderStatCard("Failed", fmt.Sprintf("%d", m.state.batchResult.Summary.FailedCount)),
		),
		renderSection("Controls", "Up/Down selects a case. Enter opens detail. q exits."),
	)
}

func renderSimpleScreen(step, title, subtitle, status string, sections ...string) string {
	all := []string{renderScreenHeader(step, title, status, subtitle)}
	all = append(all, sections...)
	return rootPanelStyle.Render(strings.Join(all, "\n\n"))
}

func renderScreenHeader(step, title, status, subtitle string) string {
	parts := []string{stepBadgeStyle.Render(step), titleStyle.Render(title)}
	if strings.TrimSpace(status) != "" {
		parts = append(parts, statusBadgeStyle.Render(status))
	}
	lines := []string{lipgloss.JoinHorizontal(lipgloss.Top, parts...)}
	if strings.TrimSpace(subtitle) != "" {
		lines = append(lines, subtitleStyle.Render(subtitle))
	}
	return strings.Join(lines, "\n")
}

func renderStatsRow(cards ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, cards...)
}

func renderStatCard(label, value string) string {
	return statCardStyle.Render(strings.Join([]string{
		mutedStyle.Render(strings.ToUpper(strings.TrimSpace(label))),
		statValueStyle.Render(emptyFallback(value, "-")),
	}, "\n"))
}

func renderSection(title, body string) string {
	lines := []string{sectionTitleStyle.Render(title)}
	if trimmed := strings.TrimSpace(body); trimmed != "" {
		lines = append(lines, body)
	}
	return sectionStyle.Render(strings.Join(lines, "\n"))
}

func renderSearchInput(query string) string {
	value := strings.TrimSpace(query)
	if value == "" {
		return inputBoxStyle.Render("> " + inputPlaceholderStyle.Render("type any word from chat_name or chat_id"))
	}
	return inputBoxStyle.Render("> " + inputValueStyle.Render(value))
}

func renderVisibleCountLine(total, cursor int) string {
	if total == 0 {
		return "No matches yet. Adjust the search text or clear it to browse recent active chats."
	}
	start, end := visibleWindow(total, cursor, maxVisibleChatItems)
	return fmt.Sprintf("Showing %d-%d of %d matches", start+1, end, total)
}

func renderChatCandidates(candidates []replay.ChatCandidate, cursor int) string {
	if len(candidates) == 0 {
		return mutedStyle.Render("No chat candidates available in the loaded catalog.")
	}

	start, end := visibleWindow(len(candidates), cursor, maxVisibleChatItems)
	lines := make([]string, 0, maxVisibleChatItems+2)
	if start > 0 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("... %d earlier matches hidden", start)))
	}

	for i := start; i < end; i++ {
		item := candidates[i]
		name := truncateText(emptyFallback(strings.TrimSpace(item.ChatName), "<unknown>"), 34)
		chatID := truncateText(strings.TrimSpace(item.ChatID), 26)
		meta := fmt.Sprintf("messages=%d | last=%s", item.MessageCountInWindow, emptyFallback(strings.TrimSpace(item.LastMessageAt), "-"))
		row := strings.Join([]string{
			fmt.Sprintf("%s  %s", name, mutedStyle.Render("("+chatID+")")),
			mutedStyle.Render(meta),
		}, "\n")
		if i == cursor {
			lines = append(lines, selectedRowStyle.Render(row))
			continue
		}
		lines = append(lines, listRowStyle.Render(row))
	}

	if end < len(candidates) {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("... %d later matches hidden", len(candidates)-end)))
	}
	return strings.Join(lines, "\n")
}

func visibleWindow(total, cursor, size int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if size <= 0 || total <= size {
		return 0, total
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}

	start := cursor - size/2
	if start < 0 {
		start = 0
	}
	end := start + size
	if end > total {
		end = total
		start = end - size
	}
	return start, end
}

func truncateText(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max || max <= 0 {
		return string(runes)
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func selectedChatName(candidate *replay.ChatCandidate) string {
	if candidate == nil {
		return ""
	}
	return strings.TrimSpace(candidate.ChatName)
}
