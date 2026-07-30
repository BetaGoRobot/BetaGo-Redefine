package agentcard

import (
	"reflect"
	"sort"
	"testing"
)

func TestCatalogContainsEveryV1ComponentWithSafeKnowledge(t *testing.T) {
	catalog := NewCatalog()
	entries := catalog.Discover(CatalogFilter{Version: VersionV1})
	wantNames := []string{
		"button",
		"cancel",
		"columns",
		"divider",
		"facts",
		"markdown",
		"multi_select",
		"note",
		"plain_text",
		"reset",
		"section",
		"single_select",
		"submit",
		"text_input",
	}
	gotNames := make([]string, len(entries))
	for index, entry := range entries {
		gotNames[index] = entry.Name
		if entry.Version != VersionV1 || entry.Category == "" ||
			entry.Purpose == "" || len(entry.Fields) == 0 ||
			entry.BudgetCost <= 0 || entry.SafeExample == "" ||
			entry.DisallowedUse == "" || len(entry.LifecycleCompatibility) == 0 {
			t.Fatalf("incomplete catalog entry: %#v", entry)
		}
	}
	if !sort.StringsAreSorted(gotNames) {
		t.Fatalf("catalog output is not deterministic: %#v", gotNames)
	}
	sort.Strings(wantNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("catalog names = %#v, want %#v", gotNames, wantNames)
	}
}

func TestCatalogDiscoverFiltersDeterministically(t *testing.T) {
	catalog := NewCatalog()
	first := catalog.Discover(CatalogFilter{
		Version:  VersionV1,
		Category: CategoryInput,
	})
	second := catalog.Discover(CatalogFilter{
		Version:  VersionV1,
		Category: CategoryInput,
	})
	if !reflect.DeepEqual(first, second) || len(first) != 3 {
		t.Fatalf("input discovery = %#v / %#v", first, second)
	}
	one := catalog.Discover(CatalogFilter{
		Version: VersionV1,
		Name:    "single_select",
	})
	if len(one) != 1 || one[0].Name != "single_select" {
		t.Fatalf("name discovery = %#v", one)
	}
	if got := catalog.Discover(CatalogFilter{
		Version: "agent-card/v999",
	}); len(got) != 0 {
		t.Fatalf("unknown version discovery = %#v", got)
	}
}
