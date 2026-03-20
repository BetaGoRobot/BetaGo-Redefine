package larkmsg

import "strings"

type CollapsiblePanelOptions struct {
	ElementID       string
	Expanded        bool
	Padding         string
	Margin          string
	VerticalSpacing string
}

func CollapsiblePanel(title string, elements []any, opts CollapsiblePanelOptions) map[string]any {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "详情"
	}

	panel := map[string]any{
		"tag":              "collapsible_panel",
		"expanded":         opts.Expanded,
		"vertical_spacing": defaultString(opts.VerticalSpacing, "4px"),
		"padding":          defaultString(opts.Padding, "8px"),
		"elements":         elements,
		"header": map[string]any{
			"title":          Markdown(title),
			"width":          "full",
			"vertical_align": "center",
			"padding":        "4px 0px 4px 8px",
			"icon": map[string]any{
				"tag":   "standard_icon",
				"token": "down-small-ccm_outlined",
				"size":  "16px 16px",
			},
			"icon_position":       "follow_text",
			"icon_expanded_angle": -180,
		},
		"border": map[string]any{
			"color":         "grey",
			"corner_radius": "5px",
		},
	}
	if opts.Margin != "" {
		panel["margin"] = opts.Margin
	}
	if elementID := normalizeElementID(opts.ElementID); elementID != "" {
		panel["element_id"] = elementID
	}
	return panel
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
