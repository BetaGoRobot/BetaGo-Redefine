package initialcore

// WakeupAction names a initial-run core logic type.
type WakeupAction string

const (
	WakeupActionClear      WakeupAction = "clear"
	WakeupActionSkipBusy   WakeupAction = "skip_busy"
	WakeupActionReschedule WakeupAction = "reschedule"
)

// WakeupDecisionInput carries initial-run core logic state.
type WakeupDecisionInput struct {
	PendingCount         int64
	ActiveExecutionCount int64
	MaxExecutionPerScope int64
}

// DecideWakeupAction implements initial-run core logic behavior.
func DecideWakeupAction(input WakeupDecisionInput) WakeupAction {
	if input.PendingCount <= 0 {
		return WakeupActionClear
	}
	if input.ActiveExecutionCount >= input.MaxExecutionPerScope {
		return WakeupActionSkipBusy
	}
	return WakeupActionReschedule
}
