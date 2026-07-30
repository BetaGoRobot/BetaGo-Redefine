package agentcard

import (
	"encoding/json"
	"fmt"
	"sort"
)

const RuntimeResumeAction = "agent.runtime.resume"

type LifecycleState string

const (
	LifecycleInteractive LifecycleState = "interactive"
	LifecycleSubmitted   LifecycleState = "submitted"
	LifecycleProcessing  LifecycleState = "processing"
	LifecycleResolved    LifecycleState = "resolved"
	LifecycleExpired     LifecycleState = "expired"
	LifecycleFailed      LifecycleState = "failed"
)

type RuntimeBinding struct {
	RunID             string
	StepID            string
	InteractionID     string
	Revision          int64
	Token             string
	InteractionKind   string
	TrustedCapability json.RawMessage
}

type BoundCardSpec struct {
	spec     CardSpec
	state    LifecycleState
	bindings map[string]RuntimeBinding
}

func NewBoundCardSpec(
	spec CardSpec,
	state LifecycleState,
	bindings map[string]RuntimeBinding,
) (*BoundCardSpec, error) {
	if len(ValidateCardSpec(spec)) != 0 {
		return nil, fmt.Errorf("cannot bind invalid card spec")
	}
	if !validLifecycleState(state) {
		return nil, fmt.Errorf("invalid card lifecycle state %q", state)
	}
	cloned, err := cloneSpec(spec)
	if err != nil {
		return nil, err
	}
	copied := make(map[string]RuntimeBinding, len(bindings))
	for actionID, binding := range bindings {
		binding.TrustedCapability = append(
			json.RawMessage(nil),
			binding.TrustedCapability...,
		)
		copied[actionID] = binding
	}
	return &BoundCardSpec{spec: cloned, state: state, bindings: copied}, nil
}

func (s *BoundCardSpec) Spec() CardSpec {
	if s == nil {
		return CardSpec{}
	}
	cloned, _ := cloneSpec(s.spec)
	return cloned
}

func (s *BoundCardSpec) State() LifecycleState {
	if s == nil {
		return ""
	}
	return s.state
}

func (s *BoundCardSpec) CallbackPayload(action Action) (map[string]any, error) {
	if s == nil {
		return nil, fmt.Errorf("bound card spec is nil")
	}
	binding, ok := s.bindings[action.ID]
	if !ok {
		return nil, fmt.Errorf("card action %q is not runtime-bound", action.ID)
	}
	for name, value := range map[string]string{
		"run_id": binding.RunID, "step_id": binding.StepID,
		"interaction_id": binding.InteractionID, "token": binding.Token,
		"interaction_kind": binding.InteractionKind,
	} {
		if value == "" {
			return nil, fmt.Errorf("card action %q has incomplete %s binding", action.ID, name)
		}
	}
	if binding.Revision <= 0 {
		return nil, fmt.Errorf("card action %q has invalid revision", action.ID)
	}
	return map[string]any{
		"action":           RuntimeResumeAction,
		"run_id":           binding.RunID,
		"step_id":          binding.StepID,
		"interaction_id":   binding.InteractionID,
		"revision":         binding.Revision,
		"token":            binding.Token,
		"interaction_kind": binding.InteractionKind,
		"continue_agent":   action.Mode != ActionModeServer,
		"action_id":        action.ID,
	}, nil
}

type RedactedBoundCardSpec struct {
	Spec     CardSpec          `json:"spec"`
	State    LifecycleState    `json:"state"`
	Bindings []RedactedBinding `json:"bindings"`
}

type RedactedBinding struct {
	ActionID        string `json:"action_id"`
	RunID           string `json:"run_id"`
	StepID          string `json:"step_id"`
	InteractionID   string `json:"interaction_id"`
	Revision        int64  `json:"revision"`
	Token           string `json:"token"`
	InteractionKind string `json:"interaction_kind"`
}

func (s *BoundCardSpec) Redacted() RedactedBoundCardSpec {
	if s == nil {
		return RedactedBoundCardSpec{}
	}
	result := RedactedBoundCardSpec{
		Spec: s.Spec(), State: s.state,
		Bindings: make([]RedactedBinding, 0, len(s.bindings)),
	}
	for actionID, binding := range s.bindings {
		result.Bindings = append(result.Bindings, RedactedBinding{
			ActionID: actionID, RunID: binding.RunID, StepID: binding.StepID,
			InteractionID: binding.InteractionID, Revision: binding.Revision,
			Token: "[REDACTED]", InteractionKind: binding.InteractionKind,
		})
	}
	sort.Slice(result.Bindings, func(i, j int) bool {
		return result.Bindings[i].ActionID < result.Bindings[j].ActionID
	})
	return result
}

func (s *BoundCardSpec) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Redacted())
}

func (s *BoundCardSpec) String() string {
	encoded, err := json.Marshal(s.Redacted())
	if err != nil {
		return `{"error":"redaction_failed"}`
	}
	return string(encoded)
}

func cloneSpec(spec CardSpec) (CardSpec, error) {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return CardSpec{}, fmt.Errorf("clone card spec: %w", err)
	}
	var cloned CardSpec
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return CardSpec{}, fmt.Errorf("clone card spec: %w", err)
	}
	return cloned, nil
}

func validLifecycleState(state LifecycleState) bool {
	switch state {
	case LifecycleInteractive, LifecycleSubmitted, LifecycleProcessing,
		LifecycleResolved, LifecycleExpired, LifecycleFailed:
		return true
	default:
		return false
	}
}
