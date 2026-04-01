package tui

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay"
	"github.com/charmbracelet/bubbles/progress"
)

type RunnerProgress struct {
	Completed int
	Total     int
	Success   int
	Failed    int
}

type State struct {
	screen              Screen
	searchQuery         string
	errText             string
	allChatCandidates   []replay.ChatCandidate
	chatCandidates      []replay.ChatCandidate
	chatCursor          int
	selectedChat        *replay.ChatCandidate
	filters             replay.SampleFilterOptions
	samples             []replay.ReplaySample
	selectedSamples     map[string]bool
	sampleCursor        int
	progress            RunnerProgress
	progressBar         progress.Model
	batchResult         *replay.ReplayBatchResult
	reportView          ReportView
	reportCursor        int
	activeCaseMessageID string
}

func newState(config Config) State {
	if config.Days <= 0 {
		config.Days = 7
	}
	if config.Limit <= 0 {
		config.Limit = 20
	}
	return State{
		screen:          ScreenChatPicker,
		searchQuery:     "",
		selectedSamples: make(map[string]bool),
		progressBar:     progress.New(progress.WithWidth(24)),
		reportView:      ReportViewSummary,
		filters: replay.SampleFilterOptions{
			Days:  config.Days,
			Limit: config.Limit,
		},
	}
}
