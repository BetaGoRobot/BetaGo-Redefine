package agentruntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultCapabilityApprovalTTL = 15 * time.Minute

type CapabilityApprovalSpec struct {
	Type      string    `json:"type,omitempty"`
	Title     string    `json:"title,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type CapabilityCallInput struct {
	Request      CapabilityRequest            `json:"request"`
	Approval     *CapabilityApprovalSpec      `json:"approval,omitempty"`
	Continuation *CapabilityContinuationInput `json:"continuation,omitempty"`
	QueueTail    []QueuedCapabilityCall       `json:"queue_tail,omitempty"`
}

type CapabilityContinuationInput struct {
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

type ContinuationProcessorOption func(*ContinuationProcessor)

func WithCapabilityRegistry(registry *CapabilityRegistry) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.registry = registry
		}
	}
}

func decodeCapabilityCallInput(raw []byte) (CapabilityCallInput, error) {
	input := CapabilityCallInput{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return CapabilityCallInput{}, fmt.Errorf("decode capability call input: %w", err)
	}
	return input, nil
}

func encodeCapabilityResult(result CapabilityResult) []byte {
	type capabilityResultEnvelope struct {
		OutputText               string          `json:"output_text,omitempty"`
		OutputJSON               json.RawMessage `json:"output_json,omitempty"`
		ExternalRef              string          `json:"external_ref,omitempty"`
		CompatibleReplyMessageID string          `json:"compatible_reply_message_id,omitempty"`
		CompatibleReplyKind      string          `json:"compatible_reply_kind,omitempty"`
		Async                    bool            `json:"async,omitempty"`
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	if len(result.OutputJSON) > 0 {
		raw, err = json.Marshal(capabilityResultEnvelope{
			OutputText:               result.OutputText,
			OutputJSON:               json.RawMessage(result.OutputJSON),
			ExternalRef:              result.ExternalRef,
			CompatibleReplyMessageID: result.CompatibleReplyMessageID,
			CompatibleReplyKind:      result.CompatibleReplyKind,
			Async:                    result.Async,
		})
		if err != nil {
			return nil
		}
	}
	return raw
}

func resolveCapabilityApprovalSpec(step *AgentStep, meta CapabilityMeta, input CapabilityCallInput, now time.Time) CapabilityApprovalSpec {
	spec := CapabilityApprovalSpec{}
	if input.Approval != nil {
		spec = *input.Approval
	}
	if strings.TrimSpace(spec.Type) == "" {
		spec.Type = "capability"
	}
	if strings.TrimSpace(spec.Title) == "" {
		spec.Title = "审批执行能力"
		if strings.TrimSpace(step.CapabilityName) != "" {
			spec.Title = "审批执行 " + strings.TrimSpace(step.CapabilityName)
		}
	}
	if strings.TrimSpace(spec.Summary) == "" {
		spec.Summary = strings.TrimSpace(meta.Description)
		if spec.Summary == "" {
			spec.Summary = "该能力需要审批后才能继续执行。"
		}
	}
	if spec.ExpiresAt.IsZero() {
		spec.ExpiresAt = now.UTC().Add(defaultCapabilityApprovalTTL)
	}
	return spec
}
