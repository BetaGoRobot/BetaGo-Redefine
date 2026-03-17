package cardregression

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type senderStub struct {
	calls int
	last  struct {
		target ReceiveTarget
		label  string
	}
	err error
}

func (s *senderStub) Send(ctx context.Context, target ReceiveTarget, built *BuiltCard) (string, error) {
	s.calls++
	s.last.target = target
	if built != nil {
		s.last.label = built.Label
	}
	if s.err != nil {
		return "", s.err
	}
	return "om_regression_test", nil
}

func TestRunnerRunSceneDefaultsToSmokeDefaultAndSkipsSendOnDryRun(t *testing.T) {
	registry := NewRegistry()
	calledCase := ""
	if err := registry.Register(testScene{
		key:   "schedule.list",
		cases: []CardRegressionCase{{Name: "smoke-default", Tags: []string{"smoke"}}},
		buildFunc: func(_ context.Context, req TestCardBuildRequest) (*BuiltCard, error) {
			calledCase = req.Case.Name
			return &BuiltCard{Mode: BuiltCardModeCardJSON, Label: "schedule.list", CardJSON: map[string]any{"ok": true}}, nil
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runner := NewRunner(registry, &senderStub{})
	result, err := runner.RunScene(context.Background(), RunSceneOptions{SceneKey: "schedule.list", DryRun: true})
	if err != nil {
		t.Fatalf("RunScene() error = %v", err)
	}
	if calledCase != "smoke-default" {
		t.Fatalf("expected default case smoke-default, got %q", calledCase)
	}
	if !result.Built || result.Sent {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunnerRunSuiteFiltersCasesByTag(t *testing.T) {
	registry := NewRegistry()
	seen := make([]string, 0, 4)
	if err := registry.Register(testScene{
		key: "music.list",
		cases: []CardRegressionCase{
			{Name: "smoke-default", Tags: []string{"smoke"}},
			{Name: "live-default", Tags: []string{"live"}, Requires: CardRequirementSet{NeedBusinessChatID: true}},
		},
		buildFunc: func(_ context.Context, req TestCardBuildRequest) (*BuiltCard, error) {
			seen = append(seen, req.Case.Name)
			return &BuiltCard{Mode: BuiltCardModeCardJSON, Label: req.Case.Name, CardJSON: map[string]any{"case": req.Case.Name}}, nil
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runner := NewRunner(registry, &senderStub{})
	results, err := runner.RunSuite(context.Background(), RunSuiteOptions{Suite: SuiteSmoke, DryRun: true})
	if err != nil {
		t.Fatalf("RunSuite() error = %v", err)
	}
	if len(results) != 1 || len(seen) != 1 || seen[0] != "smoke-default" {
		t.Fatalf("expected only smoke case, got results=%d seen=%v", len(results), seen)
	}
}

func TestRunnerRunSceneReturnsValidationErrorWhenRequirementsMissing(t *testing.T) {
	registry := NewRegistry()
	buildCalls := 0
	if err := registry.Register(testScene{
		key:   "config.list",
		cases: []CardRegressionCase{{Name: "live-default", Tags: []string{"live"}, Requires: CardRequirementSet{NeedBusinessChatID: true, NeedActorOpenID: true}}},
		buildFunc: func(_ context.Context, req TestCardBuildRequest) (*BuiltCard, error) {
			buildCalls++
			return &BuiltCard{Mode: BuiltCardModeCardJSON, Label: req.Case.Name, CardJSON: map[string]any{"case": req.Case.Name}}, nil
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runner := NewRunner(registry, &senderStub{})
	result, err := runner.RunScene(context.Background(), RunSceneOptions{SceneKey: "config.list", CaseName: "live-default", DryRun: true})
	if err != nil {
		t.Fatalf("RunScene() error = %v", err)
	}
	if buildCalls != 0 {
		t.Fatalf("expected requirement failure before BuildTestCard, buildCalls=%d", buildCalls)
	}
	if result.ErrorKind != ErrorKindValidation || !strings.Contains(result.Error, "business chat_id") || !strings.Contains(result.Error, "actor_open_id") {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func TestRunnerLiveSmokeTreatsMissingRequirementsAsSoftFailure(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testScene{
		key:   "config.list",
		cases: []CardRegressionCase{{Name: "live-default", Tags: []string{"live"}, Requires: CardRequirementSet{NeedBusinessChatID: true}}},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runner := NewRunner(registry, &senderStub{})
	results, err := runner.RunSuite(context.Background(), RunSuiteOptions{Suite: SuiteLiveSmoke, DryRun: true})
	if err != nil {
		t.Fatalf("RunSuite() error = %v", err)
	}
	if len(results) != 1 || results[0].ErrorKind != ErrorKindValidation {
		t.Fatalf("unexpected live-smoke result: %+v", results)
	}
}

func TestRunnerRunSceneReportsSendFailure(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testScene{
		key:   "schedule.list",
		cases: []CardRegressionCase{{Name: "smoke-default", Tags: []string{"smoke"}}},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	runner := NewRunner(registry, &senderStub{err: errors.New("send failed")})
	result, err := runner.RunScene(context.Background(), RunSceneOptions{SceneKey: "schedule.list", Target: &ReceiveTarget{ReceiveIDType: "chat_id", ReceiveID: "oc_test"}})
	if err != nil {
		t.Fatalf("RunScene() error = %v", err)
	}
	if result.ErrorKind != ErrorKindSend || !strings.Contains(result.Error, "send failed") {
		t.Fatalf("unexpected send failure result: %+v", result)
	}
}
