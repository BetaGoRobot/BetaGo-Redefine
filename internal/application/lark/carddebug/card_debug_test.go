package carddebug

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

type testRegressionScene struct {
	key       string
	cases     []cardregression.CardRegressionCase
	buildFunc func(context.Context, cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error)
}

func (s testRegressionScene) SceneKey() string { return s.key }
func (s testRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{Name: s.key}
}
func (s testRegressionScene) BuildCard(context.Context, cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return &cardregression.BuiltCard{Mode: cardregression.BuiltCardModeCardJSON, Label: s.key, CardJSON: map[string]any{"scene": s.key}}, nil
}
func (s testRegressionScene) TestCases() []cardregression.CardRegressionCase { return s.cases }
func (s testRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	if s.buildFunc != nil {
		return s.buildFunc(ctx, req)
	}
	return &cardregression.BuiltCard{Mode: cardregression.BuiltCardModeCardJSON, Label: s.key, CardJSON: map[string]any{"case": req.Case.Name}}, nil
}

func TestResolveReceiveTarget(t *testing.T) {
	target, err := ResolveReceiveTarget("", "ou_debug_user", "")
	if err != nil {
		t.Fatalf("ResolveReceiveTarget returned error: %v", err)
	}
	if target.ReceiveIDType != "open_id" {
		t.Fatalf("expected open_id, got %q", target.ReceiveIDType)
	}
	if target.ReceiveID != "ou_debug_user" {
		t.Fatalf("unexpected receive id %q", target.ReceiveID)
	}

	target, err = ResolveReceiveTarget("", "", "oc_debug_chat")
	if err != nil {
		t.Fatalf("ResolveReceiveTarget fallback returned error: %v", err)
	}
	if target.ReceiveIDType != "chat_id" || target.ReceiveID != "oc_debug_chat" {
		t.Fatalf("unexpected fallback target: %+v", target)
	}
}

func TestResolveTemplate(t *testing.T) {
	info, ok := ResolveTemplate("NormalCardReplyTemplate")
	if !ok {
		t.Fatalf("expected template alias to resolve")
	}
	if info.ID == "" {
		t.Fatalf("expected resolved template id")
	}
}

func TestBuildSampleSpecs(t *testing.T) {
	ctx := context.Background()
	for _, spec := range []string{SpecRateLimitSample, SpecScheduleSample} {
		built, err := Build(ctx, BuildRequest{Spec: spec})
		if err != nil {
			t.Fatalf("Build(%s) returned error: %v", spec, err)
		}
		if built == nil || built.Mode != BuiltCardModeCardJSON {
			t.Fatalf("expected card json for %s, got %+v", spec, built)
		}
		if len(built.CardJSON) == 0 {
			t.Fatalf("expected non-empty card json for %s", spec)
		}
	}
}

func TestBuildUsesRegisteredSceneBeforeLegacyFallback(t *testing.T) {
	previous := regressionRegistry
	registry := cardregression.NewRegistry()
	regressionRegistry = registry
	t.Cleanup(func() { regressionRegistry = previous })

	caseName := ""
	if err := registry.Register(testRegressionScene{
		key: "schedule.query",
		cases: []cardregression.CardRegressionCase{
			{Name: "live-default", Tags: []string{"live"}},
		},
		buildFunc: func(_ context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
			caseName = req.Case.Name
			return &cardregression.BuiltCard{
				Mode:     cardregression.BuiltCardModeCardJSON,
				Label:    "scene:schedule.query",
				CardJSON: map[string]any{"scene": "schedule.query"},
			}, nil
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	built, err := Build(context.Background(), BuildRequest{Spec: SpecScheduleTask})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built == nil || built.Label != "scene:schedule.query" {
		t.Fatalf("expected registry-built card, got %+v", built)
	}
	if caseName != "live-default" {
		t.Fatalf("expected alias default case live-default, got %q", caseName)
	}
}

func TestBuildUsesRegisteredSceneCaseArgs(t *testing.T) {
	previous := regressionRegistry
	registry := cardregression.NewRegistry()
	regressionRegistry = registry
	t.Cleanup(func() { regressionRegistry = previous })

	var selectedCase cardregression.CardRegressionCase
	if err := registry.Register(testRegressionScene{
		key: "config.list",
		cases: []cardregression.CardRegressionCase{
			{
				Name: "smoke-default",
				Args: map[string]string{
					"scope":        "chat",
					"selected_key": "chat_reasoning_model",
				},
				Tags: []string{"smoke"},
			},
		},
		buildFunc: func(_ context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
			selectedCase = req.Case
			return &cardregression.BuiltCard{
				Mode:     cardregression.BuiltCardModeCardJSON,
				Label:    "scene:config.list",
				CardJSON: map[string]any{"scene": "config.list"},
			}, nil
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, err := Build(context.Background(), BuildRequest{Spec: "config.list", Case: "smoke-default"}); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if selectedCase.Name != "smoke-default" {
		t.Fatalf("expected registered case to be resolved, got %+v", selectedCase)
	}
	if selectedCase.Args["selected_key"] != "chat_reasoning_model" {
		t.Fatalf("expected case args to be forwarded, got %+v", selectedCase.Args)
	}
}

func TestListSpecsContainsExtendedEntries(t *testing.T) {
	names := make(map[string]struct{})
	for _, spec := range ListSpecs() {
		names[spec.Name] = struct{}{}
	}
	for _, name := range []string{
		SpecScheduleList,
		SpecScheduleTask,
		SpecWordCountSample,
		SpecChunkSample,
	} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected spec %q to be listed", name)
		}
	}
}
