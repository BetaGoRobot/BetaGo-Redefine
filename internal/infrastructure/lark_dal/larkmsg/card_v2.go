package larkmsg

import "fmt"

type CardV2Options struct {
	HeaderTemplate  string
	VerticalSpacing string
	Padding         string
	UpdateMulti     *bool
}

type CardTextOptions struct {
	Size  string
	Color string
	Align string
}

type ColumnOptions struct {
	Width           string
	Weight          int
	VerticalAlign   string
	BackgroundStyle string
	Padding         string
	VerticalSpacing string
}

type ColumnSetOptions struct {
	HorizontalSpacing string
	FlexMode          string
	HorizontalAlign   string
	BackgroundStyle   string
	Margin            string
}

type ButtonOptions struct {
	Type           string
	Size           string
	Name           string
	FormActionType string
	Payload        map[string]any
}

func NewCardV2(title string, elements []any, opts CardV2Options) RawCard {
	headerTemplate := opts.HeaderTemplate
	if headerTemplate == "" {
		headerTemplate = "blue"
	}
	verticalSpacing := opts.VerticalSpacing
	if verticalSpacing == "" {
		verticalSpacing = "8px"
	}
	updateMulti := true
	if opts.UpdateMulti != nil {
		updateMulti = *opts.UpdateMulti
	}

	body := map[string]any{
		"vertical_spacing": verticalSpacing,
		"elements":         elements,
	}
	if opts.Padding != "" {
		body["padding"] = opts.Padding
	}

	return RawCard{
		"schema": "2.0",
		"config": map[string]any{
			"update_multi": updateMulti,
		},
		"header": map[string]any{
			"template": headerTemplate,
			"title":    PlainText(title),
		},
		"body": body,
	}
}

func PlainText(content string) map[string]any {
	return map[string]any{
		"tag":     "plain_text",
		"content": content,
	}
}

func TextDiv(content string, opts CardTextOptions) map[string]any {
	text := PlainText(content)
	if opts.Size != "" {
		text["text_size"] = opts.Size
	}
	if opts.Color != "" {
		text["text_color"] = opts.Color
	}
	if opts.Align != "" {
		text["text_align"] = opts.Align
	}
	return map[string]any{
		"tag":  "div",
		"text": text,
	}
}

func Markdown(content string) map[string]any {
	return map[string]any{
		"tag":     "markdown",
		"content": content,
	}
}

func HintMarkdown(content string) map[string]any {
	return Markdown(fmt.Sprintf("<font color='grey'>%s</font>", content))
}

func Divider() map[string]any {
	return map[string]any{
		"tag": "hr",
	}
}

func Column(elements []any, opts ColumnOptions) map[string]any {
	column := map[string]any{
		"tag":      "column",
		"elements": elements,
	}
	if opts.Width != "" {
		column["width"] = opts.Width
	}
	if opts.Weight > 0 {
		column["weight"] = opts.Weight
	}
	if opts.VerticalAlign != "" {
		column["vertical_align"] = opts.VerticalAlign
	}
	if opts.BackgroundStyle != "" {
		column["background_style"] = opts.BackgroundStyle
	}
	if opts.Padding != "" {
		column["padding"] = opts.Padding
	}
	if opts.VerticalSpacing != "" {
		column["vertical_spacing"] = opts.VerticalSpacing
	}
	return column
}

func ColumnSet(columns []any, opts ColumnSetOptions) map[string]any {
	row := map[string]any{
		"tag":     "column_set",
		"columns": columns,
	}
	if opts.HorizontalSpacing != "" {
		row["horizontal_spacing"] = opts.HorizontalSpacing
	}
	if opts.FlexMode != "" {
		row["flex_mode"] = opts.FlexMode
	}
	if opts.HorizontalAlign != "" {
		row["horizontal_align"] = opts.HorizontalAlign
	}
	if opts.BackgroundStyle != "" {
		row["background_style"] = opts.BackgroundStyle
	}
	if opts.Margin != "" {
		row["margin"] = opts.Margin
	}
	return row
}

func Button(text string, opts ButtonOptions) map[string]any {
	button := map[string]any{
		"tag":  "button",
		"text": PlainText(text),
	}
	if opts.Type != "" {
		button["type"] = opts.Type
	}
	if opts.Size != "" {
		button["size"] = opts.Size
	}
	if opts.Name != "" {
		button["name"] = opts.Name
	}
	if opts.FormActionType != "" {
		button["form_action_type"] = opts.FormActionType
	}
	if behaviors := CallbackBehaviors(opts.Payload); len(behaviors) > 0 {
		button["behaviors"] = behaviors
	}
	return button
}

func CallbackBehaviors(payload map[string]any) []any {
	if len(payload) == 0 {
		return nil
	}
	return []any{
		map[string]any{
			"type":  "callback",
			"value": payload,
		},
	}
}

func ButtonRow(flexMode string, buttons ...map[string]any) map[string]any {
	columns := make([]any, 0, len(buttons))
	for _, button := range buttons {
		columns = append(columns, Column([]any{button}, ColumnOptions{
			Width:         "auto",
			VerticalAlign: "top",
		}))
	}

	return ColumnSet(columns, ColumnSetOptions{
		HorizontalSpacing: "8px",
		FlexMode:          flexMode,
	})
}
