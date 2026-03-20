package toolmeta

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

type DeferredApprovalOptions struct {
	ResultKey         string
	PlaceholderOutput string
	ApprovalType      string
	ApprovalTitle     string
	ApprovalSummary   string
	ApprovalExpiresAt time.Time
}

func TryRecordDeferredApproval(ctx context.Context, metaData *xhandler.BaseMetaData, toolName string, opts DeferredApprovalOptions) bool {
	behavior, ok := LookupRuntimeBehavior(strings.TrimSpace(toolName))
	if !ok || behavior.Approval == nil {
		return false
	}

	approval := *behavior.Approval
	if strings.TrimSpace(opts.ResultKey) != "" {
		approval.ResultKey = strings.TrimSpace(opts.ResultKey)
	}
	if strings.TrimSpace(opts.PlaceholderOutput) != "" {
		approval.PlaceholderOutput = strings.TrimSpace(opts.PlaceholderOutput)
	}
	if strings.TrimSpace(opts.ApprovalType) != "" {
		approval.ApprovalType = strings.TrimSpace(opts.ApprovalType)
	}
	if strings.TrimSpace(opts.ApprovalTitle) != "" {
		approval.ApprovalTitle = strings.TrimSpace(opts.ApprovalTitle)
	}
	expiresAt := opts.ApprovalExpiresAt.UTC()
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(15 * time.Minute)
	}
	if strings.TrimSpace(approval.ApprovalType) == "" {
		approval.ApprovalType = "capability"
	}
	if strings.TrimSpace(approval.PlaceholderOutput) == "" {
		return false
	}

	if !runtimecontext.RecordDeferredToolCall(ctx, runtimecontext.DeferredToolCall{
		PlaceholderOutput: approval.PlaceholderOutput,
		ApprovalType:      approval.ApprovalType,
		ApprovalTitle:     approval.ApprovalTitle,
		ApprovalSummary:   strings.TrimSpace(opts.ApprovalSummary),
		ApprovalExpiresAt: expiresAt,
	}) {
		return false
	}
	if metaData != nil && strings.TrimSpace(approval.ResultKey) != "" {
		metaData.SetExtra(approval.ResultKey, approval.PlaceholderOutput)
	}
	return true
}
