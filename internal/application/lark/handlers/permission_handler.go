package handlers

import (
	"context"
	"errors"

	apppermission "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/permission"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type PermissionManageArgs struct {
	OpenID string `json:"user_id"`
}

type permissionManageHandler struct{}

var PermissionManage permissionManageHandler

const permissionManageToolResultKey = "permission_manage_result"

func (permissionManageHandler) ParseCLI(args []string) (PermissionManageArgs, error) {
	argMap, _ := parseArgs(args...)
	return PermissionManageArgs{
		OpenID: argMap["user_id"],
	}, nil
}

func (permissionManageHandler) ParseTool(raw string) (PermissionManageArgs, error) {
	if raw == "" || raw == "{}" {
		return PermissionManageArgs{}, nil
	}
	parsed := PermissionManageArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return PermissionManageArgs{}, err
	}
	return parsed, nil
}

func (permissionManageHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "permission_manage",
		Desc: "查看当前机器人支持的权限点，并交互式地为用户授予或撤销权限",
		Params: arktools.NewParams("object").
			AddProp("user_id", &arktools.Prop{
				Type: "string",
				Desc: "要查看或管理的目标用户 OpenID，不填则默认当前用户",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(permissionManageToolResultKey)
			return result
		},
	}
}

func (permissionManageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg PermissionManageArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	actorOpenID := currentOpenID(data, metaData)
	if actorOpenID == "" {
		return errors.New("operator user_id is required")
	}

	targetOpenID := arg.OpenID
	if targetOpenID == "" {
		targetOpenID = actorOpenID
	}

	cardData, err := apppermission.BuildPermissionCardJSONWithOptions(ctx, currentChatID(data, metaData), actorOpenID, targetOpenID, apppermission.PermissionCardViewOptions{
		LastModifierOpenID: actorOpenID,
	})
	if err != nil {
		return err
	}
	if err := sendCompatibleCardJSON(ctx, data, metaData, cardData, "_permissionManage", false); err != nil {
		return err
	}
	metaData.SetExtra(permissionManageToolResultKey, "权限管理卡片已发送")
	return nil
}

func resolvePermissionManageApprovalSummary(actorOpenID, targetOpenID string) string {
	if targetOpenID == "" || targetOpenID == actorOpenID {
		return "将发送当前用户的权限管理卡片"
	}
	return "将发送目标用户的权限管理卡片"
}

func (permissionManageHandler) CommandDescription() string {
	return "查看权限点并交互式授权"
}

func (permissionManageHandler) CommandExamples() []string {
	return []string{
		"/permission",
		"/permission --user_id=ou_xxx",
	}
}
