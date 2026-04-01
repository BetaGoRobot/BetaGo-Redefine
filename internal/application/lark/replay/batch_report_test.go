package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayBatchWriterCreatesArtifactLayout(t *testing.T) {
	root := t.TempDir()
	writer := ReplayBatchReportWriter{
		now: func() time.Time {
			return time.Date(2026, 3, 31, 11, 22, 33, 0, time.UTC)
		},
	}

	result, err := writer.Write(root, ReplayBatchResult{
		Request: ReplayBatchRequest{
			ChatID:   "oc_chat",
			ChatName: "Alpha Beta群",
			Days:     7,
			Filters: SampleFilterOptions{
				ChatID:          "oc_chat",
				Days:            7,
				Limit:           5,
				RequireQuestion: true,
			},
			Samples: []ReplaySample{
				{MessageID: "om_1", ChatID: "oc_chat", RawMessage: "今天怎么推进？"},
			},
		},
		Summary: ReplayBatchSummary{
			ChatID:              "oc_chat",
			ChatName:            "Alpha Beta群",
			TimeWindowDays:      7,
			SelectedSampleCount: 1,
			SuccessCount:        1,
		},
		Cases: []ReplayBatchCaseResult{
			{
				Sample: ReplaySample{MessageID: "om_1", ChatID: "oc_chat", RawMessage: "今天怎么推进？"},
				Status: ReplayBatchCaseStatusSuccess,
				Report: &ReplayReport{
					Target: ReplayTarget{ChatID: "oc_chat", MessageID: "om_1", Text: "今天怎么推进？"},
					Diff:   ReplayDiff{IntentInputChanged: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if !strings.Contains(result.ArtifactDir, filepath.Join("artifacts", "replay-batches")) {
		t.Fatalf("ArtifactDir = %q, want replay-batches path", result.ArtifactDir)
	}

	wantFiles := []string{
		filepath.Join(result.ArtifactDir, "summary.json"),
		filepath.Join(result.ArtifactDir, "summary.md"),
		filepath.Join(result.ArtifactDir, "filters.json"),
		filepath.Join(result.ArtifactDir, "samples.json"),
		filepath.Join(result.ArtifactDir, "cases", "om_1.json"),
		filepath.Join(result.ArtifactDir, "cases", "om_1.md"),
	}
	for _, path := range wantFiles {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected artifact %q: %v", path, statErr)
		}
	}

	summaryMarkdown, readErr := os.ReadFile(filepath.Join(result.ArtifactDir, "summary.md"))
	if readErr != nil {
		t.Fatalf("ReadFile(summary.md) error = %v", readErr)
	}
	if !strings.Contains(string(summaryMarkdown), "success_count: 1") {
		t.Fatalf("summary.md = %q, want success count", string(summaryMarkdown))
	}
}
