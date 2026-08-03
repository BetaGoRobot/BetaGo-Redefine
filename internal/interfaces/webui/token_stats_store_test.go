package webui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTokenStatsGroupByRejectsColumnsOutsideAllowlist(t *testing.T) {
	store := &tokenStatsStore{}
	_, err := store.groupBy(context.Background(), "oc_chat", time.Now(), "model; DROP TABLE llm_token_usage_records")
	if err == nil || !strings.Contains(err.Error(), "unsupported token group column") {
		t.Fatalf("groupBy() error = %v", err)
	}
}

func TestSuccessRateHandlesEmptyAndSuccessfulCalls(t *testing.T) {
	if got := successRate(0, 0); got != 0 {
		t.Fatalf("successRate(0, 0) = %v", got)
	}
	if got := successRate(3, 4); got != 0.75 {
		t.Fatalf("successRate(3, 4) = %v", got)
	}
}
