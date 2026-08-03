package schema

import "testing"

func TestDefaultMigrationsIncludeLLMUsageBusinessTaxonomy(t *testing.T) {
	const want = "20260803_llm_usage_business_taxonomy"
	count := 0
	for _, migration := range DefaultMigrations() {
		if migration.Version == want {
			count++
			if migration.NonTransactional {
				t.Fatalf("migration %s unexpectedly non-transactional", want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("migration %s count = %d, want 1", want, count)
	}
}
