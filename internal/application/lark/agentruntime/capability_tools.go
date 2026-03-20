package agentruntime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
)

type ToolCapability[T any] struct {
	meta CapabilityMeta
	unit *arktools.FunctionCallUnit[T]
	data *T
}

func NewToolCapability[T any](unit *arktools.FunctionCallUnit[T], meta CapabilityMeta, data *T) Capability {
	if meta.Name == "" && unit != nil {
		meta.Name = unit.FunctionName
	}
	if meta.Description == "" && unit != nil {
		meta.Description = unit.Description
	}
	if meta.Kind == "" {
		meta.Kind = CapabilityKindTool
	}
	if meta.DefaultTimeout <= 0 {
		meta.DefaultTimeout = 30 * time.Second
	}
	return &ToolCapability[T]{
		meta: meta,
		unit: unit,
		data: data,
	}
}

func (c *ToolCapability[T]) Meta() CapabilityMeta {
	return c.meta
}

func (c *ToolCapability[T]) Execute(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	if c == nil || c.unit == nil || c.unit.Function == nil {
		return CapabilityResult{}, fmt.Errorf("tool capability is not initialized")
	}

	raw := strings.TrimSpace(string(req.PayloadJSON))
	execCtx := runtimecontext.WithCapabilityExecutionOptions(ctx, c.meta.Name, !c.meta.AllowCompatibleOutput)
	execCtx = runtimecontext.WithCompatibleReplyRecorder(execCtx, runtimecontext.NewCompatibleReplyRecorder())
	result := c.unit.Function(execCtx, raw, arktools.FCMeta[T]{
		ChatID: req.ChatID,
		OpenID: req.ActorOpenID,
		Data:   c.data,
	})
	if result.IsErr() {
		return CapabilityResult{}, result.Err()
	}
	capabilityResult := CapabilityResult{
		OutputText: result.Value(),
	}
	if replyRef, ok := runtimecontext.LatestCompatibleReplyRef(execCtx); ok {
		capabilityResult.CompatibleReplyMessageID = strings.TrimSpace(replyRef.MessageID)
		capabilityResult.CompatibleReplyKind = strings.TrimSpace(replyRef.Kind)
	}
	return capabilityResult, nil
}

func BuildToolCapabilities[T any](
	impl *arktools.Impl[T],
	metaProvider func(*arktools.FunctionCallUnit[T]) CapabilityMeta,
	data *T,
) []Capability {
	if impl == nil {
		return nil
	}

	names := make([]string, 0, len(impl.FunctionCallMap))
	for name := range impl.FunctionCallMap {
		names = append(names, name)
	}
	sort.Strings(names)

	capabilities := make([]Capability, 0, len(names))
	for _, name := range names {
		unit, ok := impl.Get(name)
		if !ok || unit == nil {
			continue
		}
		meta := CapabilityMeta{
			Name:                  unit.FunctionName,
			Description:           unit.Description,
			Kind:                  CapabilityKindTool,
			SideEffectLevel:       defaultToolSideEffectLevel(unit.FunctionName),
			RequiresApproval:      defaultToolRequiresApproval(unit.FunctionName),
			AllowCompatibleOutput: defaultToolAllowCompatibleOutput(unit.FunctionName),
			AllowedScopes:         defaultToolScopes(unit.FunctionName),
			DefaultTimeout:        30 * time.Second,
		}
		if metaProvider != nil {
			override := metaProvider(unit)
			meta = mergeCapabilityMeta(meta, override)
		}
		capabilities = append(capabilities, NewToolCapability(unit, meta, data))
	}
	return capabilities
}

func defaultToolSideEffectLevel(name string) SideEffectLevel {
	return SideEffectLevel(toolmeta.SideEffectLevelOf(name))
}

func defaultToolScopes(name string) []CapabilityScope {
	switch name {
	case "create_schedule", "list_schedules", "query_schedule", "delete_schedule", "pause_schedule", "resume_schedule":
		return []CapabilityScope{CapabilityScopeGroup, CapabilityScopeSchedule}
	default:
		return []CapabilityScope{CapabilityScopeGroup, CapabilityScopeP2P}
	}
}

func defaultToolAllowCompatibleOutput(name string) bool {
	return toolmeta.AllowCompatibleOutput(name)
}

func defaultToolRequiresApproval(name string) bool {
	return toolmeta.RequiresApproval(name)
}

func mergeCapabilityMeta(base, override CapabilityMeta) CapabilityMeta {
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Kind != "" {
		base.Kind = override.Kind
	}
	if override.Description != "" {
		base.Description = override.Description
	}
	if override.SideEffectLevel != "" {
		base.SideEffectLevel = override.SideEffectLevel
	}
	base.AllowCompatibleOutput = base.AllowCompatibleOutput || override.AllowCompatibleOutput
	if override.DefaultTimeout > 0 {
		base.DefaultTimeout = override.DefaultTimeout
	}
	if override.AllowedScopes != nil {
		base.AllowedScopes = override.AllowedScopes
	}
	base.RequiresApproval = base.RequiresApproval || override.RequiresApproval
	base.SupportsStreaming = base.SupportsStreaming || override.SupportsStreaming
	base.SupportsAsync = base.SupportsAsync || override.SupportsAsync
	base.SupportsSchedule = base.SupportsSchedule || override.SupportsSchedule
	base.Idempotent = base.Idempotent || override.Idempotent
	return base
}
