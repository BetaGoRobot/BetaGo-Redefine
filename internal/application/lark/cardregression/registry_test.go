package cardregression

import (
	"context"
	"strings"
	"testing"
)

type testScene struct {
	key       string
	meta      CardSceneMeta
	cases     []CardRegressionCase
	buildFunc func(context.Context, TestCardBuildRequest) (*BuiltCard, error)
}

func (s testScene) SceneKey() string                { return s.key }
func (s testScene) Meta() CardSceneMeta             { return s.meta }
func (s testScene) TestCases() []CardRegressionCase { return s.cases }
func (s testScene) BuildCard(context.Context, CardBuildRequest) (*BuiltCard, error) {
	return &BuiltCard{Mode: BuiltCardModeCardJSON, Label: s.key, CardJSON: map[string]any{"scene": s.key}}, nil
}
func (s testScene) BuildTestCard(ctx context.Context, req TestCardBuildRequest) (*BuiltCard, error) {
	if s.buildFunc != nil {
		return s.buildFunc(ctx, req)
	}
	return &BuiltCard{Mode: BuiltCardModeCardJSON, Label: s.key + ":" + req.Case.Name, CardJSON: map[string]any{"scene": s.key, "case": req.Case.Name}}, nil
}

func TestRegistryRejectsSceneWithoutCases(t *testing.T) {
	registry := NewRegistry()

	err := registry.Register(testScene{key: "schedule.list"})
	if err == nil || !strings.Contains(err.Error(), "at least one regression case") {
		t.Fatalf("expected missing case error, got %v", err)
	}
}

func TestRegistryRejectsDuplicateSceneKey(t *testing.T) {
	registry := NewRegistry()
	scene := testScene{key: "schedule.list", cases: []CardRegressionCase{{Name: "smoke-default"}}}
	if err := registry.Register(scene); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	err := registry.Register(scene)
	if err == nil || !strings.Contains(err.Error(), "duplicate scene key") {
		t.Fatalf("expected duplicate key error, got %v", err)
	}
}

func TestRegistryListReturnsSortedScenes(t *testing.T) {
	registry := NewRegistry()
	for _, scene := range []testScene{
		{key: "wordchunk.detail", cases: []CardRegressionCase{{Name: "sample-default"}}},
		{key: "config.list", cases: []CardRegressionCase{{Name: "live-default"}}},
		{key: "schedule.list", cases: []CardRegressionCase{{Name: "smoke-default"}}},
	} {
		if err := registry.Register(scene); err != nil {
			t.Fatalf("Register(%q) error = %v", scene.key, err)
		}
	}

	got := registry.List()
	if len(got) != 3 {
		t.Fatalf("expected 3 scenes, got %d", len(got))
	}
	if got[0].SceneKey() != "config.list" || got[1].SceneKey() != "schedule.list" || got[2].SceneKey() != "wordchunk.detail" {
		t.Fatalf("unexpected scene order: %q, %q, %q", got[0].SceneKey(), got[1].SceneKey(), got[2].SceneKey())
	}
}

func TestValidateRegisteredScenesRejectsNilAndBlankKey(t *testing.T) {
	registry := NewRegistry()
	registry.scenes["nil"] = nil
	registry.scenes[""] = testScene{cases: []CardRegressionCase{{Name: "smoke-default"}}}

	err := registry.ValidateRegisteredScenes()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "nil scene") || !strings.Contains(err.Error(), "blank scene key") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
