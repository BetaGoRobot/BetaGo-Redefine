package conversationeval

import (
	"testing"
)

func TestSamplingAlwaysKeepsHighValueEpisodes(t *testing.T) {
	policy := SamplingPolicy{BaselineRate: 0, ContextDiffThreshold: 0.2}
	tests := []struct {
		name    string
		signals SamplingSignals
		reason  string
	}{
		{"decision disagreement", SamplingSignals{DecisionDisagreement: true}, "decision_disagreement"},
		{"context diff threshold", SamplingSignals{ContextDiffRatio: 0.2}, "context_diff"},
		{"tool", SamplingSignals{UsedTool: true}, "tool"},
		{"wait", SamplingSignals{Waited: true}, "wait"},
		{"supersede", SamplingSignals{Superseded: true}, "supersede"},
		{"feedback", SamplingSignals{HasFeedback: true}, "feedback"},
		{"error", SamplingSignals{HasError: true}, "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecideSampling(policy, "cohort-1", "event-1", test.signals)
			if err != nil {
				t.Fatalf("DecideSampling() error = %v", err)
			}
			if !got.Keep || got.Reason != test.reason {
				t.Fatalf("DecideSampling() = %#v, want keep reason %q", got, test.reason)
			}
		})
	}
}

func TestSamplingAgreeSkipIsDeterministic(t *testing.T) {
	policy := SamplingPolicy{BaselineRate: 0.5, ContextDiffThreshold: 0.2}
	first, err := DecideSampling(
		policy, "cohort-1", "anchor-1", SamplingSignals{AgreeAndSkip: true},
	)
	if err != nil {
		t.Fatalf("DecideSampling(first) error = %v", err)
	}
	for index := 0; index < 100; index++ {
		next, nextErr := DecideSampling(
			policy, "cohort-1", "anchor-1", SamplingSignals{AgreeAndSkip: true},
		)
		if nextErr != nil || next != first {
			t.Fatalf("deterministic decision[%d] = %#v, %v; want %#v", index, next, nextErr, first)
		}
	}
	if first.Reason != "agree_skip_baseline" {
		t.Fatalf("reason = %q, want agree_skip_baseline", first.Reason)
	}
}

func TestSamplingBaselineRateExtremes(t *testing.T) {
	for _, test := range []struct {
		rate float64
		keep bool
	}{
		{0, false},
		{1, true},
	} {
		got, err := DecideSampling(
			SamplingPolicy{BaselineRate: test.rate, ContextDiffThreshold: 0.2},
			"cohort-1", "anchor-1", SamplingSignals{},
		)
		if err != nil {
			t.Fatalf("DecideSampling(rate=%v) error = %v", test.rate, err)
		}
		if got.Keep != test.keep {
			t.Fatalf("DecideSampling(rate=%v).Keep = %v, want %v", test.rate, got.Keep, test.keep)
		}
	}
}
