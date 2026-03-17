package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFlagsSupportsSceneAndSuiteOptions(t *testing.T) {
	opts, err := parseFlags([]string{"--scene", "help.view", "--case", "smoke-default", "--suite", "smoke", "--dry-run", "--report-json", "/tmp/report.json"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if opts.Scene != "help.view" || opts.Case != "smoke-default" || opts.Suite != "smoke" || !opts.DryRun || opts.ReportJSON != "/tmp/report.json" {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestRenderScenesIncludesRegisteredCommandScenes(t *testing.T) {
	registerRegressionScenes()

	output := renderScenes()
	for _, key := range []string{
		"help.view",
		"command.form",
		"config.list",
		"permission.manage",
		"ratelimit.stats",
		"schedule.list",
		"schedule.query",
	} {
		if !strings.Contains(output, key) {
			t.Fatalf("expected regression scene %q in output, got: %s", key, output)
		}
	}
}

func TestBuildPlanUsesSceneRequirements(t *testing.T) {
	registerRegressionScenes()

	plan := buildPlan(options{
		Scene:  "feature.list",
		Case:   "live-default",
		DryRun: true,
	})

	if !plan.NeedDB || !plan.NeedFeatureRegistry {
		t.Fatalf("expected feature live scene to require db and feature registry, got %+v", plan)
	}
}

func TestRunSmokeSuiteWritesReportJSON(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "regression-report.json")
	if err := run(context.Background(), []string{"--suite", "smoke", "--dry-run", "--report-json", reportPath}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "help.view") || !strings.Contains(content, "command.form") {
		t.Fatalf("expected smoke suite report to include registered scenes, got: %s", content)
	}
}
