package tui

import (
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	services Services
	config   Config
	state    State
}

func NewModel(services Services) Model {
	return NewModelWithConfig(Config{}, services)
}

func NewModelWithConfig(config Config, services Services) Model {
	config = normalizeConfig(config)
	return Model{
		services: services,
		config:   config,
		state:    newState(config),
	}
}

func (m Model) Init() tea.Cmd {
	return m.loadChatCatalogCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.updateKey(msg)
	case ChatChosenMsg:
		candidate := msg.Candidate
		m.state.selectedChat = &candidate
		m.state.screen = ScreenFilterBuilder
		m.state.errText = ""
		if strings.TrimSpace(m.state.filters.ChatID) == "" {
			m.state.filters.ChatID = strings.TrimSpace(candidate.ChatID)
		}
		return m, nil
	case FiltersAppliedMsg:
		m.state.filters = msg.Filters
		m.state.samples = append([]replay.ReplaySample(nil), msg.Samples...)
		m.state.selectedSamples = make(map[string]bool, len(msg.Samples))
		m.state.sampleCursor = 0
		m.state.screen = ScreenSamplePreview
		m.state.errText = ""
		return m, nil
	case SearchResultsMsg:
		m.state.allChatCandidates = append([]replay.ChatCandidate(nil), msg.Candidates...)
		m.state.chatCandidates = filterChatCandidates(m.state.allChatCandidates, m.state.searchQuery)
		m.state.chatCursor = 0
		m.state.errText = errorText(msg.Err)
		return m, nil
	case SamplesLoadedMsg:
		if msg.Err != nil {
			m.state.errText = errorText(msg.Err)
			return m, nil
		}
		return m.Update(FiltersAppliedMsg{Filters: msg.Filters, Samples: msg.Samples})
	case ToggleSampleSelectionMsg:
		messageID := strings.TrimSpace(msg.MessageID)
		if messageID == "" {
			return m, nil
		}
		if m.state.selectedSamples[messageID] {
			delete(m.state.selectedSamples, messageID)
		} else {
			m.state.selectedSamples[messageID] = true
		}
		return m, nil
	case SelectAllSamplesMsg:
		m.state.selectedSamples = make(map[string]bool, len(m.state.samples))
		for _, item := range m.state.samples {
			if id := strings.TrimSpace(item.MessageID); id != "" {
				m.state.selectedSamples[id] = true
			}
		}
		return m, nil
	case StartBatchRunMsg:
		m.state.screen = ScreenReplayRunner
		m.state.reportView = ReportViewSummary
		return m, nil
	case BatchProgressMsg:
		m.state.progress = RunnerProgress{
			Completed: msg.Completed,
			Total:     msg.Total,
			Success:   msg.Success,
			Failed:    msg.Failed,
		}
		return m, m.state.progressBar.SetPercent(batchPercent(msg.Completed, msg.Total))
	case BatchFinishedMsg:
		result := msg.Result
		m.state.batchResult = &result
		m.state.screen = ScreenReportViewer
		m.state.reportView = ReportViewSummary
		m.state.reportCursor = 0
		m.state.activeCaseMessageID = ""
		return m, nil
	case OpenCaseDetailMsg:
		m.state.reportView = ReportViewCaseDetail
		m.state.activeCaseMessageID = strings.TrimSpace(msg.MessageID)
		return m, nil
	case BackToSummaryMsg:
		m.state.reportView = ReportViewSummary
		m.state.activeCaseMessageID = ""
		return m, nil
	default:
		var cmd tea.Cmd
		progressModel, progressCmd := m.state.progressBar.Update(msg)
		if cast, ok := progressModel.(progress.Model); ok {
			m.state.progressBar = cast
			cmd = progressCmd
		}
		return m, cmd
	}
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	switch m.state.screen {
	case ScreenChatPicker:
		return m.updateChatPickerKey(msg)
	case ScreenFilterBuilder:
		return m.updateFilterBuilderKey(msg)
	case ScreenSamplePreview:
		return m.updateSamplePreviewKey(msg)
	case ScreenReportViewer:
		return m.updateReportViewerKey(msg)
	default:
		return m, nil
	}
}

func (m Model) updateChatPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyRunes:
		m.state.searchQuery += string(msg.Runes)
		m.state.chatCandidates = filterChatCandidates(m.state.allChatCandidates, m.state.searchQuery)
		m.state.chatCursor = 0
		return m, nil
	case tea.KeyBackspace:
		runes := []rune(m.state.searchQuery)
		if len(runes) > 0 {
			m.state.searchQuery = string(runes[:len(runes)-1])
		}
		m.state.chatCandidates = filterChatCandidates(m.state.allChatCandidates, m.state.searchQuery)
		m.state.chatCursor = 0
		return m, nil
	case tea.KeyUp:
		if m.state.chatCursor > 0 {
			m.state.chatCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.state.chatCursor < len(m.state.chatCandidates)-1 {
			m.state.chatCursor++
		}
		return m, nil
	case tea.KeyEnter:
		if len(m.state.allChatCandidates) == 0 {
			return m, m.loadChatCatalogCmd()
		}
		if len(m.state.chatCandidates) == 0 {
			return m, nil
		}
		return m.Update(ChatChosenMsg{Candidate: m.state.chatCandidates[m.state.chatCursor]})
	default:
		return m, nil
	}
}

func (m Model) updateFilterBuilderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter {
		return m, m.loadSamplesCmd()
	}
	return m, nil
}

func (m Model) updateSamplePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.state.sampleCursor > 0 {
			m.state.sampleCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.state.sampleCursor < len(m.state.samples)-1 {
			m.state.sampleCursor++
		}
		return m, nil
	case tea.KeySpace:
		return m.toggleCurrentSample(), nil
	case tea.KeyEnter:
		m.state.screen = ScreenReplayRunner
		return m, m.runBatchCmd()
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "a":
			return m.Update(SelectAllSamplesMsg{})
		case " ":
			return m.toggleCurrentSample(), nil
		default:
			return m, nil
		}
	default:
		return m, nil
	}
}

func (m Model) updateReportViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.state.reportView == ReportViewCaseDetail {
		switch msg.String() {
		case "b", "esc":
			return m.Update(BackToSummaryMsg{})
		default:
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyUp:
		if m.state.reportCursor > 0 {
			m.state.reportCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.state.batchResult != nil && m.state.reportCursor < len(m.state.batchResult.Cases)-1 {
			m.state.reportCursor++
		}
		return m, nil
	case tea.KeyEnter:
		if m.state.batchResult == nil || len(m.state.batchResult.Cases) == 0 {
			return m, nil
		}
		return m.Update(OpenCaseDetailMsg{MessageID: m.state.batchResult.Cases[m.state.reportCursor].Sample.MessageID})
	default:
		return m, nil
	}
}

func (m Model) loadChatCatalogCmd() tea.Cmd {
	return func() tea.Msg {
		if m.services.LoadCatalog == nil {
			return SearchResultsMsg{Err: fmt.Errorf("chat catalog service not configured")}
		}
		candidates, err := m.services.LoadCatalog(m.config.Days, 0)
		return SearchResultsMsg{Candidates: candidates, Err: err}
	}
}

func (m Model) loadSamplesCmd() tea.Cmd {
	return func() tea.Msg {
		if m.services.SelectSamples == nil || m.state.selectedChat == nil {
			return SamplesLoadedMsg{Err: fmt.Errorf("sample selector not configured")}
		}
		filters := m.state.filters
		filters.ChatID = strings.TrimSpace(m.state.selectedChat.ChatID)
		filters.Days = m.config.Days
		filters.Limit = m.config.Limit
		samples, err := m.services.SelectSamples(filters)
		return SamplesLoadedMsg{Filters: filters, Samples: samples, Err: err}
	}
}

func (m Model) runBatchCmd() tea.Cmd {
	return func() tea.Msg {
		if m.services.RunBatch == nil || m.state.selectedChat == nil {
			return BatchFinishedMsg{Result: replay.ReplayBatchResult{}}
		}
		result, err := m.services.RunBatch(replay.ReplayBatchRequest{
			ChatID:     strings.TrimSpace(m.state.selectedChat.ChatID),
			ChatName:   strings.TrimSpace(m.state.selectedChat.ChatName),
			Days:       m.config.Days,
			Filters:    m.state.filters,
			Samples:    m.selectedSampleList(),
			RunOptions: replay.ReplayRunOptions{LiveModel: m.config.LiveModel},
		})
		if err != nil {
			m.state.errText = errorText(err)
		}
		return BatchFinishedMsg{Result: result}
	}
}

func (m Model) toggleCurrentSample() tea.Model {
	if len(m.state.samples) == 0 || m.state.sampleCursor >= len(m.state.samples) {
		return m
	}
	next, _ := m.Update(ToggleSampleSelectionMsg{MessageID: m.state.samples[m.state.sampleCursor].MessageID})
	return next
}

func (m Model) selectedSampleList() []replay.ReplaySample {
	if len(m.state.selectedSamples) == 0 {
		return append([]replay.ReplaySample(nil), m.state.samples...)
	}
	out := make([]replay.ReplaySample, 0, len(m.state.selectedSamples))
	for _, item := range m.state.samples {
		if m.state.selectedSamples[strings.TrimSpace(item.MessageID)] {
			out = append(out, item)
		}
	}
	return out
}

func batchPercent(completed, total int) float64 {
	if total <= 0 {
		return 0
	}
	if completed <= 0 {
		return 0
	}
	if completed >= total {
		return 1
	}
	return float64(completed) / float64(total)
}

func normalizeConfig(config Config) Config {
	if config.Days <= 0 {
		config.Days = 7
	}
	if config.Limit <= 0 {
		config.Limit = 20
	}
	return config
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func filterChatCandidates(candidates []replay.ChatCandidate, query string) []replay.ChatCandidate {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]replay.ChatCandidate(nil), candidates...)
	}
	out := make([]replay.ChatCandidate, 0, len(candidates))
	for _, item := range candidates {
		name := strings.ToLower(strings.TrimSpace(item.ChatName))
		chatID := strings.ToLower(strings.TrimSpace(item.ChatID))
		if strings.Contains(name, query) || strings.Contains(chatID, query) {
			out = append(out, item)
		}
	}
	return out
}
