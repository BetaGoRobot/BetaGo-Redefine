package config

import (
	"context"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

const (
	configListSceneKey    = "config.list"
	featureListSceneKey   = "feature.list"
	regressionChatID      = "oc_sample_debug_chat"
	regressionActorOpenID = "ou_sample_debug_user"
)

var configRegressionBuildMu sync.Mutex

func RegisterRegressionScenes(registry *cardregression.Registry) {
	if registry == nil {
		return
	}
	for _, scene := range []cardregression.CardSceneProtocol{
		configListRegressionScene{},
		featureListRegressionScene{},
	} {
		if _, exists := registry.Get(scene.SceneKey()); exists {
			continue
		}
		registry.MustRegister(scene)
	}
}

type configListRegressionScene struct{}

type featureListRegressionScene struct{}

func (configListRegressionScene) SceneKey() string { return configListSceneKey }

func (configListRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        configListSceneKey,
		Description: "配置管理卡回归场景",
		Tags:        []string{"schema-v2", "config"},
		Owner:       "config",
	}
}

func (configListRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{
		{
			Name:        "smoke-default",
			Description: "使用样例上下文构建配置管理卡",
			Args: map[string]string{
				"scope":         "chat",
				"selected_key":  string(KeyChatReasoningModel),
				"chat_id":       regressionChatID,
				"actor_open_id": regressionActorOpenID,
			},
			Tags: []string{"smoke"},
		},
		{
			Name:        "live-default",
			Description: "使用真实 chat/user 上下文构建配置管理卡",
			Args:        map[string]string{"scope": "chat"},
			Requires: cardregression.CardRequirementSet{
				NeedBusinessChatID: true,
				NeedActorOpenID:    true,
				NeedDB:             true,
			},
			Tags: []string{"live"},
		},
	}
}

func (s configListRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, req.Args, false)
}

func (s configListRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, mergeRegressionArgs(req.Case.Args, req.Args), strings.TrimSpace(req.Case.Name) == "smoke-default")
}

func (configListRegressionScene) build(ctx context.Context, business cardregression.CardBusinessContext, args map[string]string, smoke bool) (*cardregression.BuiltCard, error) {
	scope := firstNonEmpty(args["scope"], business.Scope, "chat")
	chatID := firstNonEmpty(business.ChatID, args["chat_id"])
	actorOpenID := firstNonEmpty(business.ActorOpenID, args["actor_open_id"])
	if smoke {
		return buildSmokeConfigCard(ctx, scope, firstNonEmpty(chatID, regressionChatID), firstNonEmpty(actorOpenID, regressionActorOpenID), strings.TrimSpace(args["selected_key"]))
	}
	card, err := BuildConfigCardJSONWithOptions(ctx, scope, chatID, actorOpenID, ConfigCardViewOptions{
		SelectedKey: strings.TrimSpace(args["selected_key"]),
	})
	if err != nil {
		return nil, err
	}
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    configListSceneKey,
		CardJSON: card,
	}, nil
}

func (featureListRegressionScene) SceneKey() string { return featureListSceneKey }

func (featureListRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        featureListSceneKey,
		Description: "功能开关卡回归场景",
		Tags:        []string{"schema-v2", "config"},
		Owner:       "config",
	}
}

func (featureListRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{
		{
			Name:        "smoke-default",
			Description: "使用样例上下文构建功能开关卡",
			Args: map[string]string{
				"chat_id":       regressionChatID,
				"actor_open_id": regressionActorOpenID,
			},
			Tags: []string{"smoke"},
		},
		{
			Name:        "live-default",
			Description: "使用真实上下文构建功能开关卡",
			Requires: cardregression.CardRequirementSet{
				NeedBusinessChatID:  true,
				NeedActorOpenID:     true,
				NeedDB:              true,
				NeedFeatureRegistry: true,
			},
			Tags: []string{"live"},
		},
	}
}

func (s featureListRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, req.Args, false)
}

func (s featureListRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, mergeRegressionArgs(req.Case.Args, req.Args), strings.TrimSpace(req.Case.Name) == "smoke-default")
}

func (featureListRegressionScene) build(ctx context.Context, business cardregression.CardBusinessContext, args map[string]string, smoke bool) (*cardregression.BuiltCard, error) {
	chatID := firstNonEmpty(business.ChatID, args["chat_id"])
	actorOpenID := firstNonEmpty(business.ActorOpenID, args["actor_open_id"])
	if smoke {
		return buildSmokeFeatureCard(ctx, firstNonEmpty(chatID, regressionChatID), firstNonEmpty(actorOpenID, regressionActorOpenID))
	}
	card, err := BuildFeatureCard(ctx, chatID, actorOpenID)
	if err != nil {
		return nil, err
	}
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    featureListSceneKey,
		CardJSON: map[string]any(card),
	}, nil
}

func mergeRegressionArgs(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	for k, v := range override {
		merged[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return merged
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func buildSmokeConfigCard(ctx context.Context, scope, chatID, actorOpenID, selectedKey string) (*cardregression.BuiltCard, error) {
	return withConfigRegressionIsolation(func() (*cardregression.BuiltCard, error) {
		card, err := BuildConfigCardJSONWithOptions(ctx, scope, chatID, actorOpenID, ConfigCardViewOptions{
			SelectedKey: selectedKey,
		})
		if err != nil {
			return nil, err
		}
		return &cardregression.BuiltCard{
			Mode:     cardregression.BuiltCardModeCardJSON,
			Label:    configListSceneKey,
			CardJSON: card,
		}, nil
	})
}

func buildSmokeFeatureCard(ctx context.Context, chatID, actorOpenID string) (*cardregression.BuiltCard, error) {
	return withConfigRegressionIsolation(func() (*cardregression.BuiltCard, error) {
		card, err := BuildFeatureCard(ctx, chatID, actorOpenID)
		if err != nil {
			return nil, err
		}
		return &cardregression.BuiltCard{
			Mode:     cardregression.BuiltCardModeCardJSON,
			Label:    featureListSceneKey,
			CardJSON: map[string]any(card),
		}, nil
	})
}

func withConfigRegressionIsolation(fn func() (*cardregression.BuiltCard, error)) (*cardregression.BuiltCard, error) {
	configRegressionBuildMu.Lock()
	defer configRegressionBuildMu.Unlock()

	oldIdentity := currentBotIdentity
	oldBaseConfig := currentBaseConfig
	defer func() {
		currentBotIdentity = oldIdentity
		currentBaseConfig = oldBaseConfig
	}()

	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	currentBaseConfig = func() *infraConfig.BaseConfig { return nil }
	return fn()
}
