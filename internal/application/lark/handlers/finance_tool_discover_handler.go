package handlers

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const financeToolDiscoverResultKey = "finance_tool_discover_result"

type financeToolDiscoverHandler struct{}

var FinanceToolDiscover financeToolDiscoverHandler

func (financeToolDiscoverHandler) ParseTool(raw string) (FinanceToolDiscoverArgs, error) {
	parsed := FinanceToolDiscoverArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return FinanceToolDiscoverArgs{}, err
	}
	return parsed, nil
}

func (financeToolDiscoverHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "finance_tool_discover",
		Desc: "发现可用的只读金融/经济工具，返回 tool_name、schema 和示例，供下一轮按需调用。",
		Params: arktools.NewParams("object").
			AddProp("query", &arktools.Prop{
				Type: "string",
				Desc: "按工具名、描述或示例做模糊搜索",
			}).
			AddProp("category", &arktools.Prop{
				Type: "string",
				Desc: "按分类过滤，支持 market、news、economy",
			}).
			AddProp("tool_names", &arktools.Prop{
				Type: "array",
				Desc: "按显式 tool_name 白名单过滤",
			}).
			AddProp("limit", &arktools.Prop{
				Type: "number",
				Desc: "最多返回多少个工具，默认不过滤",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(financeToolDiscoverResultKey)
			return result
		},
	}
}

func (financeToolDiscoverHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg FinanceToolDiscoverArgs) error {
	catalog := aktool.FinanceToolCatalog()
	allowed := make(map[string]struct{}, len(arg.ToolNames))
	for _, name := range arg.ToolNames {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	query := strings.ToLower(strings.TrimSpace(arg.Query))
	category := strings.ToLower(strings.TrimSpace(arg.Category))

	items := make([]FinanceToolDiscoverItem, 0, len(catalog))
	for _, def := range catalog {
		if category != "" && strings.ToLower(string(def.Category)) != category {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[def.Name]; !ok {
				continue
			}
		}
		if query != "" {
			searchSpace := strings.ToLower(def.Name + "\n" + def.Description + "\n" + string(def.Category) + "\n" + strings.Join(def.Examples, "\n"))
			if !strings.Contains(searchSpace, query) {
				continue
			}
		}

		required := make([]string, 0)
		if def.Schema != nil {
			required = append(required, def.Schema.Required...)
		}
		items = append(items, FinanceToolDiscoverItem{
			ToolName:    def.Name,
			Description: def.Description,
			Schema:      def.Schema,
			Required:    required,
			Examples:    append([]string(nil), def.Examples...),
			Categories:  []string{string(def.Category)},
		})
		if arg.Limit > 0 && len(items) >= arg.Limit {
			break
		}
	}

	metaData.SetExtra(financeToolDiscoverResultKey, utils.MustMarshalString(FinanceToolDiscoverResult{Tools: items}))
	return nil
}
