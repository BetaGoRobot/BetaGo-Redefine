package tui

import (
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay"
	tea "github.com/charmbracelet/bubbletea"
)

func TestReplayTUIStateTransitions(t *testing.T) {
	model := NewModel(Services{})
	model.state.chatCandidates = []replay.ChatCandidate{
		{ChatID: "oc_chat", ChatName: "Alpha"},
	}

	next, _ := model.Update(ChatChosenMsg{Candidate: model.state.chatCandidates[0]})
	model = next.(Model)
	if model.state.screen != ScreenFilterBuilder {
		t.Fatalf("screen after chat choose = %v, want filter builder", model.state.screen)
	}

	next, _ = model.Update(FiltersAppliedMsg{
		Filters: replay.SampleFilterOptions{ChatID: "oc_chat", Days: 3, Limit: 2},
		Samples: []replay.ReplaySample{
			{MessageID: "om_1", ChatID: "oc_chat", RawMessage: "今天怎么推进？"},
		},
	})
	model = next.(Model)
	if model.state.screen != ScreenSamplePreview {
		t.Fatalf("screen after filters = %v, want sample preview", model.state.screen)
	}

	next, _ = model.Update(StartBatchRunMsg{})
	model = next.(Model)
	if model.state.screen != ScreenReplayRunner {
		t.Fatalf("screen after run start = %v, want runner", model.state.screen)
	}

	next, _ = model.Update(BatchFinishedMsg{
		Result: replay.ReplayBatchResult{
			Summary: replay.ReplayBatchSummary{SuccessCount: 1},
			Cases: []replay.ReplayBatchCaseResult{
				{Sample: replay.ReplaySample{MessageID: "om_1"}},
			},
		},
	})
	model = next.(Model)
	if model.state.screen != ScreenReportViewer {
		t.Fatalf("screen after batch finish = %v, want report viewer", model.state.screen)
	}
}

func TestReplayTUISelectionTogglesAndSelectAll(t *testing.T) {
	model := NewModel(Services{})
	model.state.screen = ScreenSamplePreview
	model.state.samples = []replay.ReplaySample{
		{MessageID: "om_1", ChatID: "oc_chat"},
		{MessageID: "om_2", ChatID: "oc_chat"},
	}

	next, _ := model.Update(ToggleSampleSelectionMsg{MessageID: "om_1"})
	model = next.(Model)
	if !model.state.selectedSamples["om_1"] {
		t.Fatalf("om_1 should be selected")
	}

	next, _ = model.Update(ToggleSampleSelectionMsg{MessageID: "om_1"})
	model = next.(Model)
	if model.state.selectedSamples["om_1"] {
		t.Fatalf("om_1 should be deselected")
	}

	next, _ = model.Update(SelectAllSamplesMsg{})
	model = next.(Model)
	if len(model.state.selectedSamples) != 2 {
		t.Fatalf("selected count = %d, want 2", len(model.state.selectedSamples))
	}
}

func TestReplayTUIRunnerProgressUpdates(t *testing.T) {
	model := NewModel(Services{})
	model.state.screen = ScreenReplayRunner

	next, cmd := model.Update(BatchProgressMsg{Completed: 2, Total: 5, Success: 1, Failed: 1})
	model = next.(Model)
	if model.state.progress.Completed != 2 || model.state.progress.Total != 5 {
		t.Fatalf("progress = %+v, want completed=2 total=5", model.state.progress)
	}
	if model.state.progress.Success != 1 || model.state.progress.Failed != 1 {
		t.Fatalf("progress counts = %+v", model.state.progress)
	}
	if cmd == nil {
		t.Fatalf("progress update should return command for progress animation")
	}
}

func TestReplayTUIReportViewerSwitchesBetweenSummaryAndCaseDetail(t *testing.T) {
	model := NewModel(Services{})
	model.state.screen = ScreenReportViewer
	model.state.batchResult = &replay.ReplayBatchResult{
		Summary: replay.ReplayBatchSummary{SuccessCount: 1},
		Cases: []replay.ReplayBatchCaseResult{
			{Sample: replay.ReplaySample{MessageID: "om_1", RawMessage: "今天怎么推进？"}},
		},
	}

	next, _ := model.Update(OpenCaseDetailMsg{MessageID: "om_1"})
	model = next.(Model)
	if model.state.reportView != ReportViewCaseDetail {
		t.Fatalf("reportView = %v, want case detail", model.state.reportView)
	}
	if model.state.activeCaseMessageID != "om_1" {
		t.Fatalf("activeCaseMessageID = %q, want om_1", model.state.activeCaseMessageID)
	}

	next, _ = model.Update(BackToSummaryMsg{})
	model = next.(Model)
	if model.state.reportView != ReportViewSummary {
		t.Fatalf("reportView = %v, want summary", model.state.reportView)
	}

	view := model.View()
	for _, want := range []string{"Report Summary", "SUCCESS", "Controls"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() = %q, want summary content %q", view, want)
		}
	}
}

func TestReplayTUIKeyboardFlowLoadsChatsSamplesAndBatch(t *testing.T) {
	model := NewModelWithConfig(Config{Days: 3, Limit: 5}, Services{
		LoadCatalog: func(days, limit int) ([]replay.ChatCandidate, error) {
			if days != 3 || limit != 0 {
				t.Fatalf("LoadCatalog() got %d/%d", days, limit)
			}
			return []replay.ChatCandidate{
				{ChatID: "oc_chat", ChatName: "Alpha"},
				{ChatID: "oc_other", ChatName: "Other"},
			}, nil
		},
		SelectSamples: func(options replay.SampleFilterOptions) ([]replay.ReplaySample, error) {
			if options.ChatID != "oc_chat" || options.Days != 3 || options.Limit != 5 {
				t.Fatalf("SelectSamples() got %+v", options)
			}
			return []replay.ReplaySample{{MessageID: "om_1", ChatID: "oc_chat", RawMessage: "今天怎么推进？"}}, nil
		},
		RunBatch: func(req replay.ReplayBatchRequest) (replay.ReplayBatchResult, error) {
			if req.ChatID != "oc_chat" || len(req.Samples) != 1 {
				t.Fatalf("RunBatch() got %+v", req)
			}
			return replay.ReplayBatchResult{
				Summary: replay.ReplayBatchSummary{SuccessCount: 1},
				Cases: []replay.ReplayBatchCaseResult{
					{Sample: replay.ReplaySample{MessageID: "om_1"}},
				},
			}, nil
		},
	})

	initCmd := model.Init()
	if initCmd == nil {
		t.Fatalf("Init() should preload chat catalog")
	}
	var cmd tea.Cmd
	next, _ := model.Update(initCmd())
	model = next.(Model)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Alpha")})
	model = next.(Model)
	if len(model.state.chatCandidates) != 1 {
		t.Fatalf("chatCandidates = %d, want 1", len(model.state.chatCandidates))
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	if model.state.screen != ScreenFilterBuilder {
		t.Fatalf("screen = %v, want filter builder", model.state.screen)
	}
	if cmd != nil {
		t.Fatalf("choose chat should not emit command")
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	sampleMsg := cmd()
	next, _ = model.Update(sampleMsg)
	model = next.(Model)
	if model.state.screen != ScreenSamplePreview {
		t.Fatalf("screen = %v, want sample preview", model.state.screen)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	model = next.(Model)
	if len(model.state.selectedSamples) != 1 {
		t.Fatalf("selected samples = %d, want 1", len(model.state.selectedSamples))
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)
	if model.state.screen != ScreenReplayRunner {
		t.Fatalf("screen = %v, want replay runner", model.state.screen)
	}
	finishedMsg := cmd()
	next, _ = model.Update(finishedMsg)
	model = next.(Model)
	if model.state.screen != ScreenReportViewer {
		t.Fatalf("screen = %v, want report viewer", model.state.screen)
	}
}

func TestReplayTUIQuitKeysReturnQuitCommand(t *testing.T) {
	model := NewModel(Services{})

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = next.(Model)
	if cmd == nil {
		t.Fatalf("quit key should return command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit command should emit tea.QuitMsg")
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+c should return command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c should emit tea.QuitMsg")
	}
}

func TestReplayTUIViewShowsVisibleHelpAndQuery(t *testing.T) {
	model := NewModel(Services{})
	model.state.searchQuery = "Alpha"
	model.state.allChatCandidates = []replay.ChatCandidate{
		{ChatID: "oc_chat", ChatName: "Alpha", MessageCountInWindow: 12, LastMessageAt: "2026-04-01 10:00:00"},
	}
	model.state.chatCandidates = []replay.ChatCandidate{
		{ChatID: "oc_chat", ChatName: "Alpha", MessageCountInWindow: 12, LastMessageAt: "2026-04-01 10:00:00"},
	}

	view := model.View()
	for _, want := range []string{
		"Chat Picker",
		"Step 1/5",
		"Local Catalog",
		"Search Chat Name / ID",
		"Catalog",
		"1 chats",
		"Showing 1-1 of 1 matches",
		"> Alpha",
		"Alpha",
		"(oc_chat)",
		"q or Ctrl+C exits the replay TUI.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() = %q, want contain %q", view, want)
		}
	}
}

func TestReplayTUIViewShowsInputPlaceholderWhenQueryIsEmpty(t *testing.T) {
	model := NewModel(Services{})
	model.state.allChatCandidates = []replay.ChatCandidate{
		{ChatID: "oc_recent", ChatName: "Recent Group"},
	}
	model.state.chatCandidates = append([]replay.ChatCandidate(nil), model.state.allChatCandidates...)

	view := model.View()
	for _, want := range []string{
		"type any word from chat_name or chat_id",
		"Empty query keeps recent active",
		"chats visible.",
		"Showing 1-1 of 1 matches",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() = %q, want contain %q", view, want)
		}
	}
}

func TestReplayTUIEmptyQueryEnterLoadsRecentChats(t *testing.T) {
	model := NewModelWithConfig(Config{Days: 3, Limit: 5}, Services{
		LoadCatalog: func(days, limit int) ([]replay.ChatCandidate, error) {
			if days != 3 || limit != 0 {
				t.Fatalf("LoadCatalog() got %d/%d", days, limit)
			}
			return []replay.ChatCandidate{{ChatID: "oc_recent", ChatName: "Recent Group"}}, nil
		},
	})

	cmd := model.Init()
	if cmd == nil {
		t.Fatalf("Init() should preload recent chats")
	}
	next, _ := model.Update(cmd())
	model = next.(Model)
	if len(model.state.chatCandidates) != 1 || model.state.chatCandidates[0].ChatID != "oc_recent" {
		t.Fatalf("chatCandidates = %+v, want recent list", model.state.chatCandidates)
	}
}
