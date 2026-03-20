package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

type agenticDeferredApprovalSpec struct {
	ToolName          string
	ResultKey         string
	PlaceholderOutput string
	ApprovalType      string
	ApprovalTitle     string
	ApprovalSummary   string
	ApprovalExpiresAt time.Time
}

func tryDeferAgenticApproval(ctx context.Context, metaData *xhandler.BaseMetaData, spec agenticDeferredApprovalSpec) bool {
	if strings.TrimSpace(spec.ToolName) != "" {
		return toolmeta.TryRecordDeferredApproval(ctx, metaData, spec.ToolName, toolmeta.DeferredApprovalOptions{
			ResultKey:         spec.ResultKey,
			PlaceholderOutput: spec.PlaceholderOutput,
			ApprovalType:      spec.ApprovalType,
			ApprovalTitle:     spec.ApprovalTitle,
			ApprovalSummary:   spec.ApprovalSummary,
			ApprovalExpiresAt: spec.ApprovalExpiresAt,
		})
	}
	if strings.TrimSpace(spec.PlaceholderOutput) == "" {
		return false
	}
	if strings.TrimSpace(spec.ApprovalType) == "" {
		spec.ApprovalType = "capability"
	}
	if spec.ApprovalExpiresAt.IsZero() {
		spec.ApprovalExpiresAt = time.Now().UTC().Add(15 * time.Minute)
	}
	if !runtimecontext.RecordDeferredToolCall(ctx, runtimecontext.DeferredToolCall{
		PlaceholderOutput: spec.PlaceholderOutput,
		ApprovalType:      spec.ApprovalType,
		ApprovalTitle:     spec.ApprovalTitle,
		ApprovalSummary:   spec.ApprovalSummary,
		ApprovalExpiresAt: spec.ApprovalExpiresAt,
	}) {
		return false
	}
	if metaData != nil && strings.TrimSpace(spec.ResultKey) != "" {
		metaData.SetExtra(spec.ResultKey, spec.PlaceholderOutput)
	}
	return true
}
