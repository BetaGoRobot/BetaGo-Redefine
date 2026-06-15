package mcpbridge

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestRegisterAddsAllowedTools(t *testing.T) {
	ins := arktools.New[larkim.P2MessageReceiveV1]()
	Register(ins, RegisterOptions{Policies: luckin.ToolPolicies()})
	specs := ins.Tools()

	foundCreateOrder := false
	foundPrepare := false
	for _, spec := range specs {
		if spec.GetToolFunction() == nil {
			continue
		}
		name := spec.GetToolFunction().Name
		if name == "createOrder" {
			foundCreateOrder = true
		}
		if name == "luckin_order_prepare_create" {
			foundPrepare = true
		}
	}
	if foundCreateOrder {
		t.Fatalf("raw createOrder was registered")
	}
	if !foundPrepare {
		t.Fatalf("prepare-create tool missing")
	}
	unit, ok := ins.Get("luckin_order_prepare_create")
	if !ok {
		t.Fatalf("prepare-create unit missing")
	}
	if unit.Parameters == nil || !unit.Parameters.AdditionalProperties {
		t.Fatalf("prepare-create tool params should allow raw MCP arguments")
	}
}
