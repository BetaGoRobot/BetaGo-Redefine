package replay

import (
	"context"
	"strings"
)

type ReplayBatchCaseStatus string

const (
	ReplayBatchCaseStatusSuccess ReplayBatchCaseStatus = "success"
	ReplayBatchCaseStatusPartial ReplayBatchCaseStatus = "partial"
	ReplayBatchCaseStatusFailed  ReplayBatchCaseStatus = "failed"
)

type ReplayBatchRequest struct {
	ChatID     string              `json:"chat_id"`
	ChatName   string              `json:"chat_name"`
	Days       int                 `json:"days"`
	Filters    SampleFilterOptions `json:"filters"`
	Samples    []ReplaySample      `json:"samples"`
	RunOptions ReplayRunOptions    `json:"run_options"`
}

type ReplayBatchSummary struct {
	ChatID                 string `json:"chat_id"`
	ChatName               string `json:"chat_name"`
	TimeWindowDays         int    `json:"time_window_days"`
	SelectedSampleCount    int    `json:"selected_sample_count"`
	SuccessCount           int    `json:"success_count"`
	PartialCount           int    `json:"partial_count"`
	FailedCount            int    `json:"failed_count"`
	BaselineStandardCount  int    `json:"baseline_standard_count"`
	BaselineAgenticCount   int    `json:"baseline_agentic_count"`
	AugmentedStandardCount int    `json:"augmented_standard_count"`
	AugmentedAgenticCount  int    `json:"augmented_agentic_count"`
	RouteChangedCount      int    `json:"route_changed_count"`
	GenerationChangedCount int    `json:"generation_changed_count"`
	ToolIntentChangedCount int    `json:"tool_intent_changed_count"`
}

type ReplayBatchCaseResult struct {
	Sample ReplaySample          `json:"sample"`
	Status ReplayBatchCaseStatus `json:"status"`
	Error  string                `json:"error,omitempty"`
	Report *ReplayReport         `json:"report,omitempty"`
}

type ReplayBatchResult struct {
	Request     ReplayBatchRequest      `json:"request"`
	Summary     ReplayBatchSummary      `json:"summary"`
	Cases       []ReplayBatchCaseResult `json:"cases"`
	ArtifactDir string                  `json:"artifact_dir,omitempty"`
}

type replayBatchFunc func(context.Context, string, string, ReplayRunOptions) (ReplayReport, error)

type ReplayBatchRunner struct {
	replay replayBatchFunc
}

func (r ReplayBatchRunner) Run(ctx context.Context, req ReplayBatchRequest) (ReplayBatchResult, error) {
	req = normalizeReplayBatchRequest(req)
	out := ReplayBatchResult{
		Request: req,
		Summary: ReplayBatchSummary{
			ChatID:              req.ChatID,
			ChatName:            req.ChatName,
			TimeWindowDays:      req.Days,
			SelectedSampleCount: len(req.Samples),
		},
		Cases: make([]ReplayBatchCaseResult, 0, len(req.Samples)),
	}

	replayFn := r.replayer()
	for _, sample := range req.Samples {
		chatID := firstNonEmpty(strings.TrimSpace(sample.ChatID), req.ChatID)
		report, replayErr := replayFn(ctx, chatID, strings.TrimSpace(sample.MessageID), req.RunOptions)
		caseResult := ReplayBatchCaseResult{
			Sample: sample,
			Status: replayBatchCaseStatus(report, replayErr),
			Report: replayBatchReportPtr(report),
			Error:  replayBatchErrorText(replayErr),
		}
		out.Cases = append(out.Cases, caseResult)
		accumulateReplayBatchSummary(&out.Summary, caseResult)
	}

	return out, nil
}

func (r ReplayBatchRunner) replayer() replayBatchFunc {
	if r.replay != nil {
		return r.replay
	}
	service := IntentReplayService{}
	return service.Replay
}

func normalizeReplayBatchRequest(req ReplayBatchRequest) ReplayBatchRequest {
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatName = strings.TrimSpace(req.ChatName)
	if req.Days <= 0 {
		req.Days = 7
	}
	return req
}

func replayBatchCaseStatus(report ReplayReport, err error) ReplayBatchCaseStatus {
	if err == nil {
		return ReplayBatchCaseStatusSuccess
	}
	if replayReportHasContent(report) {
		return ReplayBatchCaseStatusPartial
	}
	return ReplayBatchCaseStatusFailed
}

func replayBatchReportPtr(report ReplayReport) *ReplayReport {
	if !replayReportHasContent(report) {
		return nil
	}
	cloned := report
	return &cloned
}

func replayBatchErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func replayReportHasContent(report ReplayReport) bool {
	return strings.TrimSpace(report.Target.MessageID) != "" || len(report.Cases) > 0 || len(report.Diff.ChangedFields) > 0
}

func accumulateReplayBatchSummary(summary *ReplayBatchSummary, item ReplayBatchCaseResult) {
	if summary == nil {
		return
	}
	switch item.Status {
	case ReplayBatchCaseStatusSuccess:
		summary.SuccessCount++
	case ReplayBatchCaseStatusPartial:
		summary.PartialCount++
	default:
		summary.FailedCount++
	}

	if item.Report == nil {
		return
	}
	baseline, augmented, ok := replayCasePair(item.Report.Cases)
	if ok {
		switch strings.TrimSpace(baseline.RouteDecision.FinalMode) {
		case "agentic":
			summary.BaselineAgenticCount++
		case "standard":
			summary.BaselineStandardCount++
		}
		switch strings.TrimSpace(augmented.RouteDecision.FinalMode) {
		case "agentic":
			summary.AugmentedAgenticCount++
		case "standard":
			summary.AugmentedStandardCount++
		}
	}
	if item.Report.Diff.RouteChanged {
		summary.RouteChangedCount++
	}
	if item.Report.Diff.GenerationChanged {
		summary.GenerationChangedCount++
	}
	if item.Report.Diff.ToolIntentChanged {
		summary.ToolIntentChangedCount++
	}
}
