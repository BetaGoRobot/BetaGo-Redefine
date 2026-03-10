package db

import "testing"

func TestWithoutQueryCacheHandlesNilDB(t *testing.T) {
	if WithoutQueryCache(nil) != nil {
		t.Fatalf("expected nil db to stay nil")
	}
	if IsQueryCacheBypassed(nil) {
		t.Fatalf("nil db should not be marked as bypassed")
	}
}

func TestQueryWithoutCacheHandlesNilDB(t *testing.T) {
	prev := globalDB
	globalDB = nil
	defer func() {
		globalDB = prev
	}()

	q := QueryWithoutCache()
	if q != nil {
		t.Fatalf("expected nil query wrapper without global db")
	}
}
