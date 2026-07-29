package conversationeval

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const DefaultContextDiffThreshold = 0.20

type SamplingPolicy struct {
	BaselineRate         float64 `json:"baseline_rate"`
	ContextDiffThreshold float64 `json:"context_diff_threshold"`
}

func DefaultSamplingPolicy() SamplingPolicy {
	return SamplingPolicy{
		BaselineRate:         0.05,
		ContextDiffThreshold: DefaultContextDiffThreshold,
	}
}

func ParseSamplingPolicy(raw json.RawMessage) (SamplingPolicy, error) {
	policy := DefaultSamplingPolicy()
	if len(raw) == 0 {
		return policy, nil
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return SamplingPolicy{}, fmt.Errorf("decode sampling policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return SamplingPolicy{}, err
	}
	return policy, nil
}

func (p SamplingPolicy) Validate() error {
	if p.BaselineRate < 0 || p.BaselineRate > 1 {
		return contractError("sampling baseline_rate must be between 0 and 1")
	}
	if p.ContextDiffThreshold < 0 || p.ContextDiffThreshold > 1 {
		return contractError("sampling context_diff_threshold must be between 0 and 1")
	}
	return nil
}

type SamplingSignals struct {
	DecisionDisagreement bool
	ContextDiffRatio     float64
	UsedTool             bool
	Waited               bool
	Superseded           bool
	HasFeedback          bool
	HasError             bool
	AgreeAndSkip         bool
}

type SamplingDecision struct {
	Keep       bool    `json:"keep"`
	Reason     string  `json:"reason"`
	HashBucket float64 `json:"hash_bucket,omitempty"`
}

func DecideSampling(
	policy SamplingPolicy,
	cohortID string,
	anchorEventID string,
	signals SamplingSignals,
) (SamplingDecision, error) {
	if err := policy.Validate(); err != nil {
		return SamplingDecision{}, err
	}
	if err := validateID("sampling cohort_id", cohortID); err != nil {
		return SamplingDecision{}, err
	}
	if err := validateID("sampling anchor_event_id", anchorEventID); err != nil {
		return SamplingDecision{}, err
	}
	if signals.ContextDiffRatio < 0 || signals.ContextDiffRatio > 1 {
		return SamplingDecision{}, contractError("context diff ratio must be between 0 and 1")
	}
	for _, highValue := range []struct {
		keep   bool
		reason string
	}{
		{signals.DecisionDisagreement, "decision_disagreement"},
		{signals.ContextDiffRatio >= policy.ContextDiffThreshold, "context_diff"},
		{signals.UsedTool, "tool"},
		{signals.Waited, "wait"},
		{signals.Superseded, "supersede"},
		{signals.HasFeedback, "feedback"},
		{signals.HasError, "error"},
	} {
		if highValue.keep {
			return SamplingDecision{Keep: true, Reason: highValue.reason}, nil
		}
	}

	bucket := deterministicSampleBucket(cohortID, anchorEventID)
	reason := "baseline"
	if signals.AgreeAndSkip {
		reason = "agree_skip_baseline"
	}
	return SamplingDecision{
		Keep:       bucket < policy.BaselineRate,
		Reason:     reason,
		HashBucket: bucket,
	}, nil
}

func deterministicSampleBucket(cohortID, anchorEventID string) float64 {
	sum := sha256.Sum256([]byte(cohortID + "\x00" + anchorEventID))
	value := binary.BigEndian.Uint64(sum[:8])
	return float64(value>>11) / float64(uint64(1)<<53)
}
