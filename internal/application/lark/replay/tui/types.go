package tui

import "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay"

type Screen string

const (
	ScreenChatPicker    Screen = "chat_picker"
	ScreenFilterBuilder Screen = "filter_builder"
	ScreenSamplePreview Screen = "sample_preview"
	ScreenReplayRunner  Screen = "replay_runner"
	ScreenReportViewer  Screen = "report_viewer"
)

type ReportView string

const (
	ReportViewSummary    ReportView = "summary"
	ReportViewCaseDetail ReportView = "case_detail"
)

type Services struct {
	LoadCatalog   func(int, int) ([]replay.ChatCandidate, error)
	SelectSamples func(replay.SampleFilterOptions) ([]replay.ReplaySample, error)
	RunBatch      func(replay.ReplayBatchRequest) (replay.ReplayBatchResult, error)
}

type Config struct {
	Days      int
	Limit     int
	LiveModel bool
	OutputDir string
}

type ChatChosenMsg struct {
	Candidate replay.ChatCandidate
}

type FiltersAppliedMsg struct {
	Filters replay.SampleFilterOptions
	Samples []replay.ReplaySample
}

type ToggleSampleSelectionMsg struct {
	MessageID string
}

type SelectAllSamplesMsg struct{}

type StartBatchRunMsg struct{}

type BatchProgressMsg struct {
	Completed int
	Total     int
	Success   int
	Failed    int
}

type BatchFinishedMsg struct {
	Result replay.ReplayBatchResult
}

type SearchResultsMsg struct {
	Candidates []replay.ChatCandidate
	Err        error
}

type SamplesLoadedMsg struct {
	Filters replay.SampleFilterOptions
	Samples []replay.ReplaySample
	Err     error
}

type OpenCaseDetailMsg struct {
	MessageID string
}

type BackToSummaryMsg struct{}
