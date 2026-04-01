package replay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ReplayBatchReportWriter struct {
	now func() time.Time
}

func (w ReplayBatchReportWriter) Write(rootDir string, result ReplayBatchResult) (ReplayBatchResult, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		rootDir = "."
	}

	artifactDir := filepath.Join(rootDir, "artifacts", "replay-batches", replayBatchArtifactName(w.clock(), result.Request.ChatName, result.Request.ChatID))
	if err := os.MkdirAll(filepath.Join(artifactDir, "cases"), 0o755); err != nil {
		return ReplayBatchResult{}, err
	}

	files := map[string]any{
		filepath.Join(artifactDir, "summary.json"): result.Summary,
		filepath.Join(artifactDir, "filters.json"): result.Request.Filters,
		filepath.Join(artifactDir, "samples.json"): result.Request.Samples,
	}
	for path, payload := range files {
		if err := writeReplayBatchJSON(path, payload); err != nil {
			return ReplayBatchResult{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "summary.md"), []byte(renderReplayBatchSummaryMarkdown(result.Summary)), 0o644); err != nil {
		return ReplayBatchResult{}, err
	}

	for _, item := range result.Cases {
		jsonPath := filepath.Join(artifactDir, "cases", strings.TrimSpace(item.Sample.MessageID)+".json")
		mdPath := filepath.Join(artifactDir, "cases", strings.TrimSpace(item.Sample.MessageID)+".md")
		if err := writeReplayBatchJSON(jsonPath, item); err != nil {
			return ReplayBatchResult{}, err
		}
		if err := os.WriteFile(mdPath, []byte(renderReplayBatchCaseMarkdown(item)), 0o644); err != nil {
			return ReplayBatchResult{}, err
		}
	}

	result.ArtifactDir = artifactDir
	return result, nil
}

func (w ReplayBatchReportWriter) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now().UTC()
}

func replayBatchArtifactName(now time.Time, chatName, chatID string) string {
	stamp := now.UTC().Format("20060102-150405")
	slug := slugifyReplayBatchName(firstNonEmpty(chatName, chatID, "chat"))
	return stamp + "-" + slug
}

func slugifyReplayBatchName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "chat"
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func writeReplayBatchJSON(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func renderReplayBatchSummaryMarkdown(summary ReplayBatchSummary) string {
	lines := []string{
		"# Replay Batch Summary",
		fmt.Sprintf("chat_id: %s", strings.TrimSpace(summary.ChatID)),
		fmt.Sprintf("chat_name: %s", strings.TrimSpace(summary.ChatName)),
		fmt.Sprintf("time_window_days: %d", summary.TimeWindowDays),
		fmt.Sprintf("selected_sample_count: %d", summary.SelectedSampleCount),
		fmt.Sprintf("success_count: %d", summary.SuccessCount),
		fmt.Sprintf("partial_count: %d", summary.PartialCount),
		fmt.Sprintf("failed_count: %d", summary.FailedCount),
		fmt.Sprintf("baseline_standard_count: %d", summary.BaselineStandardCount),
		fmt.Sprintf("baseline_agentic_count: %d", summary.BaselineAgenticCount),
		fmt.Sprintf("augmented_standard_count: %d", summary.AugmentedStandardCount),
		fmt.Sprintf("augmented_agentic_count: %d", summary.AugmentedAgenticCount),
		fmt.Sprintf("route_changed_count: %d", summary.RouteChangedCount),
		fmt.Sprintf("generation_changed_count: %d", summary.GenerationChangedCount),
		fmt.Sprintf("tool_intent_changed_count: %d", summary.ToolIntentChangedCount),
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderReplayBatchCaseMarkdown(item ReplayBatchCaseResult) string {
	lines := []string{
		fmt.Sprintf("status: %s", item.Status),
		fmt.Sprintf("message_id: %s", strings.TrimSpace(item.Sample.MessageID)),
		fmt.Sprintf("chat_id: %s", strings.TrimSpace(item.Sample.ChatID)),
		fmt.Sprintf("raw_message: %s", strings.TrimSpace(item.Sample.RawMessage)),
	}
	if item.Error != "" {
		lines = append(lines, fmt.Sprintf("error: %s", item.Error))
	}
	if item.Report != nil {
		lines = append(lines, "", item.Report.RenderText())
	}
	return strings.Join(lines, "\n") + "\n"
}
