package initialstate

import "context"

// RunOwnership carries initial-run context state state.
type RunOwnership struct {
	TriggerType    string `json:"trigger_type,omitempty"`
	AttachToRunID  string `json:"attach_to_run_id,omitempty"`
	SupersedeRunID string `json:"supersede_run_id,omitempty"`
}

// ReplyOutputMode names a initial-run context state type.
type ReplyOutputMode string

const (
	ReplyOutputModeAgentic  ReplyOutputMode = "agentic"
	ReplyOutputModeStandard ReplyOutputMode = "standard"
)

// ReplyTargetMode names a initial-run context state type.
type ReplyTargetMode string

const (
	ReplyTargetModePatch ReplyTargetMode = "patch"
	ReplyTargetModeReply ReplyTargetMode = "reply"
)

// ReplyTarget carries initial-run context state state.
type ReplyTarget struct {
	Mode          ReplyTargetMode `json:"mode,omitempty"`
	MessageID     string          `json:"message_id,omitempty"`
	CardID        string          `json:"card_id,omitempty"`
	ReplyInThread bool            `json:"reply_in_thread,omitempty"`
}

type runOwnershipContextKey struct{}
type replyTargetContextKey struct{}

// WithRunOwnership implements initial-run context state behavior.
func WithRunOwnership(ctx context.Context, ownership RunOwnership) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ownership.TriggerType == "" && ownership.AttachToRunID == "" && ownership.SupersedeRunID == "" {
		return ctx
	}
	return context.WithValue(ctx, runOwnershipContextKey{}, ownership)
}

// RunOwnershipFromContext implements initial-run context state behavior.
func RunOwnershipFromContext(ctx context.Context) (RunOwnership, bool) {
	if ctx == nil {
		return RunOwnership{}, false
	}
	ownership, ok := ctx.Value(runOwnershipContextKey{}).(RunOwnership)
	if !ok {
		return RunOwnership{}, false
	}
	if ownership.TriggerType == "" && ownership.AttachToRunID == "" && ownership.SupersedeRunID == "" {
		return RunOwnership{}, false
	}
	return ownership, true
}

// WithReplyTarget implements initial-run context state behavior.
func WithReplyTarget(ctx context.Context, target ReplyTarget) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.Mode == "" || (target.MessageID == "" && target.CardID == "") {
		return ctx
	}
	return context.WithValue(ctx, replyTargetContextKey{}, target)
}

// ReplyTargetFromContext implements initial-run context state behavior.
func ReplyTargetFromContext(ctx context.Context) (ReplyTarget, bool) {
	if ctx == nil {
		return ReplyTarget{}, false
	}
	target, ok := ctx.Value(replyTargetContextKey{}).(ReplyTarget)
	if !ok {
		return ReplyTarget{}, false
	}
	if target.Mode == "" || (target.MessageID == "" && target.CardID == "") {
		return ReplyTarget{}, false
	}
	return target, true
}
