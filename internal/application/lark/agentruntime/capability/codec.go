package capability

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DecodeCallInput implements capability runtime behavior.
func DecodeCallInput(raw []byte) (CallInput, error) {
	input := CallInput{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return CallInput{}, fmt.Errorf("decode capability call input: %w", err)
	}
	return input, nil
}

// EncodeResult implements capability runtime behavior.
func EncodeResult(result Result) []byte {
	type resultEnvelope struct {
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
		raw, err = json.Marshal(resultEnvelope{
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

// ResolveApprovalSpec implements capability runtime behavior.
func ResolveApprovalSpec(capabilityName, description string, input CallInput, now time.Time, defaultTTL time.Duration) ApprovalSpec {
	spec := ApprovalSpec{}
	if input.Approval != nil {
		spec = *input.Approval
	}
	if strings.TrimSpace(spec.Type) == "" {
		spec.Type = "capability"
	}
	if strings.TrimSpace(spec.Title) == "" {
		spec.Title = "审批执行能力"
		if strings.TrimSpace(capabilityName) != "" {
			spec.Title = "审批执行 " + strings.TrimSpace(capabilityName)
		}
	}
	if strings.TrimSpace(spec.Summary) == "" {
		spec.Summary = strings.TrimSpace(description)
		if spec.Summary == "" {
			spec.Summary = "该能力需要审批后才能继续执行。"
		}
	}
	if spec.ExpiresAt.IsZero() {
		spec.ExpiresAt = now.UTC().Add(defaultTTL)
	}
	return spec
}
