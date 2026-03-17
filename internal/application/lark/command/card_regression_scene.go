package command

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
)

const (
	helpViewSceneKey    = "help.view"
	commandFormSceneKey = "command.form"
)

func RegisterRegressionScenes(registry *cardregression.Registry) {
	if registry == nil {
		return
	}
	for _, scene := range []cardregression.CardSceneProtocol{
		helpViewRegressionScene{},
		commandFormRegressionScene{},
	} {
		if _, exists := registry.Get(scene.SceneKey()); exists {
			continue
		}
		registry.MustRegister(scene)
	}
}

type helpViewRegressionScene struct{}

type commandFormRegressionScene struct{}

func (helpViewRegressionScene) SceneKey() string { return helpViewSceneKey }
func (helpViewRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        helpViewSceneKey,
		Description: "命令帮助卡回归场景",
		Tags:        []string{"schema-v2", "command"},
		Owner:       "command",
	}
}
func (helpViewRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{{
		Name:        "smoke-default",
		Description: "构建 config set 的帮助卡",
		Args:        map[string]string{"command": "config set"},
		Tags:        []string{"smoke"},
	}}
}
func (s helpViewRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Args)
}
func (s helpViewRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Args)
}
func (helpViewRegressionScene) build(_ context.Context, args map[string]string) (*cardregression.BuiltCard, error) {
	commandPath := strings.TrimSpace(args["command"])
	if commandPath == "" {
		commandPath = "config set"
	}
	card := BuildHelpCardJSON(NewLarkRootCommand(), commandPath)
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    helpViewSceneKey,
		CardJSON: map[string]any(card),
	}, nil
}

func (commandFormRegressionScene) SceneKey() string { return commandFormSceneKey }
func (commandFormRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        commandFormSceneKey,
		Description: "命令表单卡回归场景",
		Tags:        []string{"schema-v2", "command"},
		Owner:       "command",
	}
}
func (commandFormRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{{
		Name:        "smoke-default",
		Description: "构建 config set 的表单卡",
		Args:        map[string]string{"command": "/config set --key=intent_recognition_enabled"},
		Tags:        []string{"smoke"},
	}}
}
func (s commandFormRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Args)
}
func (s commandFormRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Args)
}
func (commandFormRegressionScene) build(_ context.Context, args map[string]string) (*cardregression.BuiltCard, error) {
	rawCommand := strings.TrimSpace(args["command"])
	if rawCommand == "" {
		rawCommand = "/config set --key=intent_recognition_enabled"
	}
	card, err := BuildCommandFormCardJSON(NewLarkRootCommand(), rawCommand)
	if err != nil {
		return nil, err
	}
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    commandFormSceneKey,
		CardJSON: map[string]any(card),
	}, nil
}
