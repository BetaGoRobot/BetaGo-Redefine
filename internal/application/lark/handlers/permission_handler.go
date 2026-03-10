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
	UserID string `json:"user_id"`
}

type permissionManageHandler struct{}

var PermissionManage permissionManageHandler

func (permissionManageHandler) ParseCLI(args []string) (PermissionManageArgs, error) {
	argMap, _ := parseArgs(args...)
	return PermissionManageArgs{
		UserID: argMap["user_id"],
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
	}
}

func (permissionManageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg PermissionManageArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	actorUserID := currentUserID(data, metaData)
	if actorUserID == "" {
		return errors.New("operator user_id is required")
	}

	targetUserID := arg.UserID
	if targetUserID == "" {
		targetUserID = actorUserID
	}

	cardData, err := apppermission.BuildPermissionCardJSON(ctx, currentChatID(data, metaData), actorUserID, targetUserID)
	if err != nil {
		return err
	}
	return sendCompatibleCardJSON(ctx, data, metaData, cardData, "_permissionManage", false)
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
