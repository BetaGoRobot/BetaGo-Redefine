package agentcard

import "sort"

type CatalogCategory string

const (
	CategoryLayout  CatalogCategory = "layout"
	CategoryContent CatalogCategory = "content"
	CategoryInput   CatalogCategory = "input"
	CategoryAction  CatalogCategory = "action"
)

type CatalogEntry struct {
	Name                   string          `json:"name"`
	Category               CatalogCategory `json:"category"`
	Version                string          `json:"version"`
	Purpose                string          `json:"purpose"`
	Fields                 []string        `json:"fields"`
	BudgetCost             int             `json:"budget_cost"`
	SafeExample            string          `json:"safe_example"`
	DisallowedUse          string          `json:"disallowed_use"`
	LifecycleCompatibility []string        `json:"lifecycle_compatibility"`
	ActionModes            []ActionMode    `json:"action_modes,omitempty"`
}

type CatalogFilter struct {
	Version  string          `json:"version,omitempty"`
	Category CatalogCategory `json:"category,omitempty"`
	Name     string          `json:"name,omitempty"`
}

type Catalog struct {
	entries []CatalogEntry
}

func NewCatalog() *Catalog {
	interactive := []string{"interactive"}
	contentLifecycle := []string{
		"interactive", "submitted", "processing", "resolved", "expired", "failed",
	}
	entries := []CatalogEntry{
		catalogAction("button", "A single semantic action trigger", []ActionMode{
			ActionModeUI, ActionModeCapabilityConfirm, ActionModeServer,
		}),
		catalogAction("cancel", "Cancel the current interaction", []ActionMode{
			ActionModeUI, ActionModeServer,
		}),
		{
			Name: "columns", Category: CategoryLayout, Version: VersionV1,
			Purpose: "Arrange shallow content in two or three columns",
			Fields:  []string{"id", "columns"}, BudgetCost: 1,
			SafeExample:            `{"kind":"columns","id":"summary","columns":[{"id":"left","blocks":[]},{"id":"right","blocks":[]}]}`,
			DisallowedUse:          "deep nesting or more than three columns",
			LifecycleCompatibility: contentLifecycle,
		},
		{
			Name: "divider", Category: CategoryLayout, Version: VersionV1,
			Purpose: "Separate adjacent semantic groups",
			Fields:  []string{"id"}, BudgetCost: 1,
			SafeExample:            `{"kind":"divider","id":"separator"}`,
			DisallowedUse:          "decorative repetition without information hierarchy",
			LifecycleCompatibility: contentLifecycle,
		},
		{
			Name: "facts", Category: CategoryContent, Version: VersionV1,
			Purpose: "Show concise label and value pairs",
			Fields:  []string{"id", "items"}, BudgetCost: 1,
			SafeExample:            `{"kind":"facts","id":"facts","items":[{"label":"Time","value":"10:00"}]}`,
			DisallowedUse:          "large tables or hidden executable values",
			LifecycleCompatibility: contentLifecycle,
		},
		{
			Name: "markdown", Category: CategoryContent, Version: VersionV1,
			Purpose: "Show formatted explanatory content",
			Fields:  []string{"id", "text"}, BudgetCost: 1,
			SafeExample:            `{"kind":"markdown","id":"body","text":"Please confirm **10:00**."}`,
			DisallowedUse:          "raw HTML, scripts, or unbounded documents",
			LifecycleCompatibility: contentLifecycle,
		},
		catalogSelect("multi_select", "Choose multiple values"),
		{
			Name: "note", Category: CategoryContent, Version: VersionV1,
			Purpose: "Show a concise warning, source, or timing hint",
			Fields:  []string{"id", "text"}, BudgetCost: 1,
			SafeExample:            `{"kind":"note","id":"warning","text":"This changes the schedule."}`,
			DisallowedUse:          "concealing mandatory or risky information",
			LifecycleCompatibility: contentLifecycle,
		},
		{
			Name: "plain_text", Category: CategoryContent, Version: VersionV1,
			Purpose: "Show literal short text without Markdown interpretation",
			Fields:  []string{"id", "text"}, BudgetCost: 1,
			SafeExample:            `{"kind":"plain_text","id":"label","text":"Schedule confirmation"}`,
			DisallowedUse:          "long formatted content",
			LifecycleCompatibility: contentLifecycle,
		},
		catalogAction("reset", "Reset fields using deterministic server behavior", []ActionMode{
			ActionModeServer,
		}),
		{
			Name: "section", Category: CategoryLayout, Version: VersionV1,
			Purpose: "Group related blocks under an optional title",
			Fields:  []string{"id", "title", "blocks"}, BudgetCost: 1,
			SafeExample:            `{"kind":"section","id":"details","title":"Details","blocks":[]}`,
			DisallowedUse:          "cyclic or deep layout trees",
			LifecycleCompatibility: contentLifecycle,
		},
		catalogSelect("single_select", "Choose exactly one value"),
		catalogAction("submit", "Submit a validated form or confirmation", []ActionMode{
			ActionModeUI, ActionModeCapabilityConfirm,
		}),
		{
			Name: "text_input", Category: CategoryInput, Version: VersionV1,
			Purpose: "Collect a short, non-sensitive text value",
			Fields: []string{
				"id", "field_id", "form_id", "label", "required", "purpose",
				"placeholder", "multiline", "min_length", "max_length",
			},
			BudgetCost:             1,
			SafeExample:            `{"kind":"text_input","id":"reason","field_id":"reason","label":"Reason","max_length":200}`,
			DisallowedUse:          "passwords, tokens, credentials, OTP, identity, or payment data",
			LifecycleCompatibility: interactive,
		},
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return &Catalog{entries: entries}
}

func catalogAction(name, purpose string, modes []ActionMode) CatalogEntry {
	return CatalogEntry{
		Name: name, Category: CategoryAction, Version: VersionV1,
		Purpose:                purpose,
		Fields:                 []string{"kind", "id", "label", "style", "mode", "intent", "form_ref"},
		BudgetCost:             1,
		SafeExample:            `{"kind":"` + name + `","id":"action","label":"Continue","mode":"ui_action","intent":"continue"}`,
		DisallowedUse:          "raw callback payloads or trusted capability parameters",
		LifecycleCompatibility: []string{"interactive"},
		ActionModes:            append([]ActionMode(nil), modes...),
	}
}

func catalogSelect(name, purpose string) CatalogEntry {
	return CatalogEntry{
		Name: name, Category: CategoryInput, Version: VersionV1,
		Purpose: purpose,
		Fields: []string{
			"id", "field_id", "form_id", "label", "required", "purpose",
			"placeholder", "options",
		},
		BudgetCost:             1,
		SafeExample:            `{"kind":"` + name + `","id":"choice","field_id":"choice","label":"Choose","options":[{"label":"A","value":"a"}]}`,
		DisallowedUse:          "unbounded, secret, or display-text-derived values",
		LifecycleCompatibility: []string{"interactive"},
	}
}

func (c *Catalog) Discover(filter CatalogFilter) []CatalogEntry {
	if c == nil {
		return nil
	}
	result := make([]CatalogEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		if filter.Version != "" && entry.Version != filter.Version {
			continue
		}
		if filter.Category != "" && entry.Category != filter.Category {
			continue
		}
		if filter.Name != "" && entry.Name != filter.Name {
			continue
		}
		entry.Fields = append([]string(nil), entry.Fields...)
		entry.LifecycleCompatibility = append(
			[]string(nil),
			entry.LifecycleCompatibility...,
		)
		entry.ActionModes = append([]ActionMode(nil), entry.ActionModes...)
		result = append(result, entry)
	}
	return result
}
