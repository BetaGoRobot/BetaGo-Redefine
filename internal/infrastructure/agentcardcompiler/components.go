package agentcardcompiler

import (
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

func compileBlocks(blocks []agentcard.Block) ([]any, error) {
	elements := make([]any, 0, len(blocks))
	for index, block := range blocks {
		compiled, err := compileBlock(block)
		if err != nil {
			return nil, fmt.Errorf("compile block %d: %w", index, err)
		}
		elements = append(elements, compiled...)
	}
	return elements, nil
}

func compileBlock(block agentcard.Block) ([]any, error) {
	switch block.Kind {
	case agentcard.BlockMarkdown:
		if block.Markdown == nil {
			return nil, fmt.Errorf("markdown payload is missing")
		}
		return []any{larkmsg.Markdown(block.Markdown.Text)}, nil
	case agentcard.BlockPlainText:
		if block.PlainText == nil {
			return nil, fmt.Errorf("plain text payload is missing")
		}
		return []any{larkmsg.TextDiv(block.PlainText.Text, larkmsg.CardTextOptions{})}, nil
	case agentcard.BlockFacts:
		if block.Facts == nil {
			return nil, fmt.Errorf("facts payload is missing")
		}
		text := ""
		for index, fact := range block.Facts.Items {
			if index > 0 {
				text += "\n"
			}
			text += "**" + fact.Label + "**：" + fact.Value
		}
		return []any{larkmsg.Markdown(text)}, nil
	case agentcard.BlockNote:
		if block.Note == nil {
			return nil, fmt.Errorf("note payload is missing")
		}
		return []any{larkmsg.HintMarkdown(block.Note.Text)}, nil
	case agentcard.BlockDivider:
		return []any{larkmsg.Divider()}, nil
	case agentcard.BlockColumns:
		if block.Columns == nil {
			return nil, fmt.Errorf("columns payload is missing")
		}
		columns := make([]any, 0, len(block.Columns.Columns))
		for _, column := range block.Columns.Columns {
			children, err := compileBlocks(column.Blocks)
			if err != nil {
				return nil, err
			}
			options := larkmsg.ColumnOptions{Width: "weighted", Weight: column.Width}
			if options.Weight <= 0 {
				options.Weight = 1
			}
			columns = append(columns, larkmsg.Column(children, options))
		}
		return []any{larkmsg.ColumnSet(columns, larkmsg.ColumnSetOptions{
			HorizontalSpacing: "8px", FlexMode: "none",
		})}, nil
	case agentcard.BlockSection:
		if block.Section == nil {
			return nil, fmt.Errorf("section payload is missing")
		}
		elements := make([]any, 0, len(block.Section.Blocks)+1)
		if block.Section.Title != "" {
			elements = append(elements, larkmsg.Markdown("**"+block.Section.Title+"**"))
		}
		children, err := compileBlocks(block.Section.Blocks)
		if err != nil {
			return nil, err
		}
		return append(elements, children...), nil
	case agentcard.BlockTextInput:
		if block.TextInput == nil {
			return nil, fmt.Errorf("text input payload is missing")
		}
		field := block.TextInput.Field
		inputOptions := larkmsg.TextInputOptions{
			Placeholder: block.TextInput.Config.Placeholder,
			Required:    &field.Required, ElementID: block.ID,
		}
		input := larkmsg.TextInput(field.FieldID, inputOptions)
		if block.TextInput.Config.Multiline {
			input = larkmsg.TextArea(field.FieldID, inputOptions)
		}
		return labeledInput(field.Label, input), nil
	case agentcard.BlockSingleSelect:
		if block.SingleSelect == nil {
			return nil, fmt.Errorf("single select payload is missing")
		}
		return labeledInput(
			block.SingleSelect.Field.Label,
			larkmsg.SelectStatic(
				block.SingleSelect.Field.FieldID,
				larkmsg.SelectStaticOptions{
					Placeholder: block.SingleSelect.Placeholder,
					Width:       "fill", Options: selectOptions(block.SingleSelect.Options),
					ElementID: block.ID,
				},
			),
		), nil
	case agentcard.BlockMultiSelect:
		if block.MultiSelect == nil {
			return nil, fmt.Errorf("multi select payload is missing")
		}
		return labeledInput(
			block.MultiSelect.Field.Label,
			larkmsg.MultiSelectStatic(
				block.MultiSelect.Field.FieldID,
				larkmsg.MultiSelectStaticOptions{
					Placeholder: block.MultiSelect.Placeholder,
					Width:       "fill", Options: selectOptions(block.MultiSelect.Options),
					ElementID: block.ID,
				},
			),
		), nil
	default:
		return nil, fmt.Errorf("unsupported block kind %q", block.Kind)
	}
}

func labeledInput(label string, input map[string]any) []any {
	if label == "" {
		return []any{input}
	}
	return []any{larkmsg.Markdown("**" + label + "**"), input}
}

func selectOptions(values []agentcard.SelectOption) []larkmsg.SelectStaticOption {
	result := make([]larkmsg.SelectStaticOption, len(values))
	for index, value := range values {
		result[index] = larkmsg.SelectStaticOption{
			Text: value.Label, Value: value.Value,
		}
	}
	return result
}

func compileActions(
	bound *agentcard.BoundCardSpec,
	actions []agentcard.Action,
) ([]any, error) {
	buttons := make([]map[string]any, 0, len(actions))
	for _, action := range actions {
		options := larkmsg.ButtonOptions{
			Type: actionStyle(action.Style), Name: action.ID,
		}
		if action.URL != "" {
			options.URL = action.URL
		} else {
			payload, err := bound.CallbackPayload(action)
			if err != nil {
				return nil, err
			}
			options.Payload = payload
		}
		if action.Kind == agentcard.ActionSubmit {
			options.FormActionType = "submit"
		} else if action.Kind == agentcard.ActionReset {
			options.FormActionType = "reset"
		}
		buttons = append(buttons, larkmsg.Button(action.Label, options))
	}
	return larkmsg.ButtonRowsWithLimit(larkmsg.ButtonRowsOptions{
		FlexMode: "none", MaxColumns: 3, HorizontalSpacing: "8px",
		VerticalAlign: "top", ColumnWidth: "auto",
	}, buttons...), nil
}

func actionStyle(style agentcard.ActionStyle) string {
	switch style {
	case agentcard.ActionStylePrimary:
		return "primary"
	case agentcard.ActionStyleDanger:
		return "danger"
	default:
		return "default"
	}
}
