package agentruntime

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

const maxInjectedFinanceTools = 3

type financeToolDiscoverOutput struct {
	Tools []struct {
		ToolName string `json:"tool_name"`
	} `json:"tools"`
}

func resolveDiscoveredFinanceTools(
	functionName string,
	output string,
	capabilityTools *arktools.Impl[larkim.P2MessageReceiveV1],
) (*arktools.Impl[larkim.P2MessageReceiveV1], bool) {
	if strings.TrimSpace(functionName) != "finance_tool_discover" {
		return nil, false
	}
	if capabilityTools == nil {
		return nil, true
	}

	var discover financeToolDiscoverOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &discover); err != nil {
		logs.L().Warn("failed to parse finance tool discover output", zap.Error(err))
		return nil, false
	}

	allowed := make(map[string]struct{})
	for _, def := range aktool.FinanceToolCatalog() {
		allowed[def.Name] = struct{}{}
	}

	selected := arktools.New[larkim.P2MessageReceiveV1]()
	added := make(map[string]struct{})
	for _, item := range discover.Tools {
		name := strings.TrimSpace(item.ToolName)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			logs.L().Warn("ignore non-finance tool from discover output", zap.String("tool_name", name))
			continue
		}
		if _, exists := added[name]; exists {
			continue
		}
		unit, ok := capabilityTools.Get(name)
		if !ok || unit == nil {
			logs.L().Warn("finance tool not found in capability registry source", zap.String("tool_name", name))
			continue
		}
		copied := *unit
		selected.Add(&copied)
		added[name] = struct{}{}
		if len(added) >= maxInjectedFinanceTools {
			break
		}
	}

	logs.L().Info("resolved finance tool injection",
		zap.Int("tool_count", len(selected.FunctionCallMap)),
		zap.Strings("tool_names", financeToolNames(selected)),
	)
	if len(selected.FunctionCallMap) == 0 {
		return nil, true
	}
	return selected, true
}

func financeToolNames(toolset *arktools.Impl[larkim.P2MessageReceiveV1]) []string {
	if toolset == nil {
		return nil
	}
	names := make([]string, 0, len(toolset.FunctionCallMap))
	for name := range toolset.FunctionCallMap {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
