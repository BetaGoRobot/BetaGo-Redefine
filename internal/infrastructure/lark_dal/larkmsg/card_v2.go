package larkmsg

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

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

type SplitColumnsOptions struct {
	Left  ColumnOptions
	Right ColumnOptions
	Row   ColumnSetOptions
}

type ButtonOptions struct {
	Type           string
	Size           string
	Name           string
	FormActionType string
	Payload        map[string]any
	URL            string
}

type TextInputOptions struct {
	Placeholder  string
	DefaultValue string
	Required     *bool
	ElementID    string
}

type SelectStaticOption struct {
	Text  string
	Value string
}

type SelectStaticOptions struct {
	Placeholder   string
	Width         string
	InitialOption string
	Options       []SelectStaticOption
	ElementID     string
}

type PersonOptions struct {
	Size       string
	ShowAvatar *bool
	ShowName   *bool
	Style      string
	Margin     string
	ElementID  string
}

type SelectPersonOptions struct {
	Placeholder   string
	Width         string
	Type          string
	InitialOption string
	Payload       map[string]any
	Options       []string
	Disabled      *bool
	Required      *bool
	Margin        string
	ElementID     string
}

func StandardPanelCardV2Options() CardV2Options {
	return CardV2Options{
		HeaderTemplate:  "wathet",
		VerticalSpacing: "8px",
		Padding:         "12px",
	}
}

func NewStandardPanelCard(ctx context.Context, title string, elements []any, footerOptions ...StandardCardFooterOptions) RawCard {
	return NewCardV2(title, AppendStandardCardFooter(ctx, elements, footerOptions...), StandardPanelCardV2Options())
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

func Person(openID string, opts PersonOptions) map[string]any {
	person := map[string]any{
		"tag":     "person",
		"user_id": strings.TrimSpace(openID),
	}
	if opts.Size != "" {
		person["size"] = opts.Size
	}
	if opts.ShowAvatar != nil {
		person["show_avatar"] = *opts.ShowAvatar
	}
	if opts.ShowName != nil {
		person["show_name"] = *opts.ShowName
	}
	if opts.Style != "" {
		person["style"] = opts.Style
	}
	if opts.Margin != "" {
		person["margin"] = opts.Margin
	}
	if opts.ElementID != "" {
		if elementID := normalizeElementID(opts.ElementID); elementID != "" {
			person["element_id"] = elementID
		}
	}
	return person
}

func SelectPerson(opts SelectPersonOptions) map[string]any {
	element := map[string]any{
		"tag": "select_person",
	}
	if opts.Placeholder != "" {
		element["placeholder"] = PlainText(opts.Placeholder)
	}
	if opts.Width != "" {
		element["width"] = opts.Width
	}
	if opts.Type != "" {
		element["type"] = opts.Type
	}
	if initialOption := strings.TrimSpace(opts.InitialOption); initialOption != "" {
		element["initial_option"] = initialOption
	}
	if behaviors := CallbackBehaviors(opts.Payload); len(behaviors) > 0 {
		element["behaviors"] = behaviors
	}
	if len(opts.Options) > 0 {
		options := make([]map[string]any, 0, len(opts.Options))
		for _, option := range opts.Options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			options = append(options, map[string]any{
				"value": option,
			})
		}
		if len(options) > 0 {
			element["options"] = options
		}
	}
	if opts.Disabled != nil {
		element["disabled"] = *opts.Disabled
	}
	if opts.Required != nil {
		element["required"] = *opts.Required
	}
	if opts.Margin != "" {
		element["margin"] = opts.Margin
	}
	if opts.ElementID != "" {
		if elementID := normalizeElementID(opts.ElementID); elementID != "" {
			element["element_id"] = elementID
		}
	}
	return element
}

func TextInput(name string, opts TextInputOptions) map[string]any {
	return textInputElement("input", name, opts)
}

func TextArea(name string, opts TextInputOptions) map[string]any {
	return textInputElement("textarea", name, opts)
}

func SelectStatic(name string, opts SelectStaticOptions) map[string]any {
	element := map[string]any{
		"tag":  "select_static",
		"name": strings.TrimSpace(name),
	}
	if opts.Placeholder != "" {
		element["placeholder"] = PlainText(opts.Placeholder)
	}
	if opts.Width != "" {
		element["width"] = opts.Width
	}
	if initialOption := strings.TrimSpace(opts.InitialOption); initialOption != "" {
		element["initial_option"] = initialOption
	}
	if len(opts.Options) > 0 {
		options := make([]map[string]any, 0, len(opts.Options))
		for _, option := range opts.Options {
			value := strings.TrimSpace(option.Value)
			if value == "" {
				continue
			}
			label := strings.TrimSpace(option.Text)
			if label == "" {
				label = value
			}
			options = append(options, map[string]any{
				"text":  PlainText(label),
				"value": value,
			})
		}
		if len(options) > 0 {
			element["options"] = options
		}
	}
	if opts.ElementID != "" {
		if elementID := normalizeElementID(opts.ElementID); elementID != "" {
			element["element_id"] = elementID
		}
	}
	return element
}

func textInputElement(tag, name string, opts TextInputOptions) map[string]any {
	element := map[string]any{
		"tag":  tag,
		"name": strings.TrimSpace(name),
	}
	if opts.Placeholder != "" {
		element["placeholder"] = PlainText(opts.Placeholder)
	}
	if defaultValue := strings.TrimSpace(opts.DefaultValue); defaultValue != "" {
		element["default_value"] = defaultValue
	}
	if opts.ElementID != "" {
		if elementID := normalizeElementID(opts.ElementID); elementID != "" {
			element["element_id"] = elementID
		}
	}
	return element
}

func normalizeElementID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	buf := make([]rune, 0, len(raw))
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_':
			buf = append(buf, r)
		default:
			buf = append(buf, '_')
		}
	}
	if len(buf) == 0 {
		return ""
	}
	if !unicode.IsLetter(buf[0]) {
		buf = append([]rune{'e'}, buf...)
	}
	if len(buf) > 20 {
		buf = buf[:20]
	}
	return string(buf)
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
	} else if behaviors := OpenURLBehaviors(opts.URL); len(behaviors) > 0 {
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

func OpenURLBehaviors(rawURL string) []any {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	return []any{
		map[string]any{
			"type":        "open_url",
			"default_url": rawURL,
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

func StringMapToAnyMap(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func SplitColumns(left, right []any, opts SplitColumnsOptions) map[string]any {
	leftOpts := normalizeSplitColumnOptions(opts.Left)
	rightOpts := normalizeSplitColumnOptions(opts.Right)
	return ColumnSet([]any{
		Column(left, leftOpts),
		Column(right, rightOpts),
	}, opts.Row)
}

func AppendSectionsWithDividers(dst []any, sections ...[]any) []any {
	result := append([]any{}, dst...)
	appended := false
	for _, section := range sections {
		if len(section) == 0 {
			continue
		}
		if appended {
			result = append(result, Divider())
		}
		result = append(result, section...)
		appended = true
	}
	return result
}

func normalizeSplitColumnOptions(opts ColumnOptions) ColumnOptions {
	if opts.Width == "" && opts.Weight > 0 {
		opts.Width = "weighted"
	}
	return opts
}
