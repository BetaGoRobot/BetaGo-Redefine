package permission

import (
	"context"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardregression"
	permissioninfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/permission"
)

const (
	permissionManageSceneKey   = "permission.manage"
	permissionRegressionChatID = "oc_sample_debug_chat"
	permissionRegressionActor  = "ou_sample_debug_user"
)

var permissionRegressionBuildMu sync.Mutex

func RegisterRegressionScenes(registry *cardregression.Registry) {
	if registry == nil {
		return
	}
	scene := permissionManageRegressionScene{}
	if _, exists := registry.Get(scene.SceneKey()); exists {
		return
	}
	registry.MustRegister(scene)
}

type permissionManageRegressionScene struct{}

func (permissionManageRegressionScene) SceneKey() string { return permissionManageSceneKey }

func (permissionManageRegressionScene) Meta() cardregression.CardSceneMeta {
	return cardregression.CardSceneMeta{
		Name:        permissionManageSceneKey,
		Description: "权限管理卡回归场景",
		Tags:        []string{"schema-v2", "permission"},
		Owner:       "permission",
	}
}

func (permissionManageRegressionScene) TestCases() []cardregression.CardRegressionCase {
	return []cardregression.CardRegressionCase{
		{
			Name:        "smoke-default",
			Description: "使用样例身份和目标用户构建权限管理卡",
			Args: map[string]string{
				"chat_id":        permissionRegressionChatID,
				"actor_open_id":  permissionRegressionActor,
				"target_open_id": permissionRegressionActor,
			},
			Tags: []string{"smoke"},
		},
		{
			Name:        "live-default",
			Description: "使用真实上下文构建权限管理卡",
			Requires: cardregression.CardRequirementSet{
				NeedBusinessChatID: true,
				NeedActorOpenID:    true,
				NeedDB:             true,
			},
			Tags: []string{"live"},
		},
	}
}

func (s permissionManageRegressionScene) BuildCard(ctx context.Context, req cardregression.CardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, req.Args, false)
}

func (s permissionManageRegressionScene) BuildTestCard(ctx context.Context, req cardregression.TestCardBuildRequest) (*cardregression.BuiltCard, error) {
	return s.build(ctx, req.Business, mergePermissionRegressionArgs(req.Case.Args, req.Args), strings.TrimSpace(req.Case.Name) == "smoke-default")
}

func (permissionManageRegressionScene) build(ctx context.Context, business cardregression.CardBusinessContext, args map[string]string, smoke bool) (*cardregression.BuiltCard, error) {
	chatID := firstNonEmptyPermission(business.ChatID, args["chat_id"])
	actorOpenID := firstNonEmptyPermission(business.ActorOpenID, args["actor_open_id"])
	targetOpenID := firstNonEmptyPermission(business.TargetOpenID, args["target_open_id"], actorOpenID)
	if smoke {
		chatID = firstNonEmptyPermission(chatID, permissionRegressionChatID)
		actorOpenID = firstNonEmptyPermission(actorOpenID, permissionRegressionActor)
		targetOpenID = firstNonEmptyPermission(targetOpenID, actorOpenID)
		return buildPermissionSmokeCard(ctx, chatID, actorOpenID, targetOpenID)
	}
	card, err := BuildPermissionCardJSON(ctx, chatID, actorOpenID, targetOpenID)
	if err != nil {
		return nil, err
	}
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    permissionManageSceneKey,
		CardJSON: card,
	}, nil
}

func buildPermissionSmokeCard(ctx context.Context, chatID, actorOpenID, targetOpenID string) (*cardregression.BuiltCard, error) {
	permissionRegressionBuildMu.Lock()
	defer permissionRegressionBuildMu.Unlock()

	oldBootstrap := currentBootstrapAdminOpen
	oldIdentity := currentBotIdentity
	oldList := permissionListBySubject
	defer func() {
		currentBootstrapAdminOpen = oldBootstrap
		currentBotIdentity = oldIdentity
		permissionListBySubject = oldList
	}()

	currentBootstrapAdminOpen = func() string { return actorOpenID }
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{
			AppID:     "cli_sample_debug",
			BotOpenID: "ou_sample_bot",
		}
	}
	permissionListBySubject = func(context.Context, permissioninfra.ListFilter) ([]permissioninfra.Grant, error) {
		return []permissioninfra.Grant{
			{
				SubjectType:     permissioninfra.SubjectTypeUser,
				SubjectID:       targetOpenID,
				PermissionPoint: permissioninfra.PermissionPointConfigWrite,
				Scope:           permissioninfra.ScopeGlobal,
				AppID:           "cli_sample_debug",
				BotOpenID:       "ou_sample_bot",
				Remark:          "sample regression grant",
			},
		}, nil
	}

	card, err := BuildPermissionCardJSON(ctx, chatID, actorOpenID, targetOpenID)
	if err != nil {
		return nil, err
	}
	return &cardregression.BuiltCard{
		Mode:     cardregression.BuiltCardModeCardJSON,
		Label:    permissionManageSceneKey,
		CardJSON: card,
	}, nil
}

func mergePermissionRegressionArgs(base, override map[string]string) map[string]string {
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

func firstNonEmptyPermission(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
