package utils

import "testing"

// TestGenUUIDStrKeepsDistinctLongSeedsUnique guards against the split-order
// dedup bug: a real Lark message ID is already ~43 chars, so seeds built as
// "<messageID><per-order-suffix>" used to be prefix-truncated before the unique
// suffix, collapsing every split-order reply card onto one Lark idempotency
// UUID (Lark then dropped all but the first). Distinct seeds must map to
// distinct UUIDs even when longer than the length budget.
func TestGenUUIDStrKeepsDistinctLongSeedsUnique(t *testing.T) {
	msgID := "om_dc13d05a86e1cf1eec54c25a0e0e8f36e17bb1c9"
	seed1 := msgID + "_luckinSplitOrder_3f5b8c2a-1111-4aaa-9bbb-000000000001"
	seed2 := msgID + "_luckinSplitOrder_7c9d1e4f-2222-4ccc-8ddd-000000000002"

	u1 := GenUUIDStr(seed1, 50)
	u2 := GenUUIDStr(seed2, 50)

	if len(u1) > 50 || len(u2) > 50 {
		t.Fatalf("uuid exceeds length budget: %d / %d", len(u1), len(u2))
	}
	if u1 == u2 {
		t.Fatalf("distinct long seeds collapsed to same uuid: %q", u1)
	}
	// Same seed within the same 2-minute bucket must stay stable (idempotency).
	if again := GenUUIDStr(seed1, 50); again != u1 {
		t.Fatalf("same seed produced different uuid: %q vs %q", u1, again)
	}
}

// TestGenUUIDStrShortSeedStaysReadable keeps the original behavior for seeds
// that fit within the budget: no hashing, plain bucketed concatenation.
func TestGenUUIDStrShortSeedStaysReadable(t *testing.T) {
	got := GenUUIDStr("_short", 50)
	if len(got) > 50 {
		t.Fatalf("short seed should not exceed budget: %q", got)
	}
	if got[len(got)-6:] != "_short" {
		t.Fatalf("short seed should be preserved verbatim: %q", got)
	}
}
