package agentcard

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestValidateCardSpecAcceptsBoundedV1Form(t *testing.T) {
	spec := validValidationSpec()
	if issues := ValidateCardSpec(spec); len(issues) != 0 {
		t.Fatalf("ValidateCardSpec() issues = %#v", issues)
	}
}

func TestValidateCardSpecReportsStableBudgetPaths(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CardSpec)
		code string
		path string
	}{
		{
			name: "blocks",
			edit: func(spec *CardSpec) {
				spec.Blocks = make([]Block, MaxBlocks+1)
				for index := range spec.Blocks {
					spec.Blocks[index] = Divider("divider_" + canonicalNumber(index))
				}
			},
			code: "blocks_limit", path: "$.blocks",
		},
		{
			name: "inputs",
			edit: func(spec *CardSpec) {
				spec.Blocks = make([]Block, MaxInputs+1)
				for index := range spec.Blocks {
					id := "field_" + canonicalNumber(index)
					spec.Blocks[index] = TextInput(id, InputField{
						FieldID: id, FormID: "form",
						Label: "Field",
					}, TextInputConfig{MaxLength: 10})
				}
			},
			code: "inputs_limit", path: "$.blocks",
		},
		{
			name: "actions",
			edit: func(spec *CardSpec) {
				spec.Actions = make([]Action, MaxActions+1)
				for index := range spec.Actions {
					spec.Actions[index] = Action{
						Kind:  ActionButton,
						ID:    "action_" + canonicalNumber(index),
						Label: "Action", Mode: ActionModeUI, Intent: "inspect",
					}
				}
			},
			code: "actions_limit", path: "$.actions",
		},
		{
			name: "columns",
			edit: func(spec *CardSpec) {
				columns := make([]Column, MaxColumns+1)
				for index := range columns {
					columns[index] = Column{ID: "column_" + canonicalNumber(index)}
				}
				spec.Blocks = []Block{Columns("layout", columns)}
			},
			code: "columns_limit", path: "$.blocks[0].columns",
		},
		{
			name: "depth",
			edit: func(spec *CardSpec) {
				spec.Blocks = []Block{Section("s1", "", []Block{
					Section("s2", "", []Block{
						Section("s3", "", []Block{
							Section("s4", "", []Block{Divider("deep")}),
						}),
					}),
				})}
			},
			code: "nesting_depth", path: "$.blocks[0].blocks[0].blocks[0].blocks[0]",
		},
		{
			name: "title",
			edit: func(spec *CardSpec) {
				spec.Title = strings.Repeat("题", MaxTitleRunes+1)
			},
			code: "title_length", path: "$.title",
		},
		{
			name: "markdown",
			edit: func(spec *CardSpec) {
				spec.Blocks = []Block{
					Markdown("body", strings.Repeat("x", MaxMarkdownRunes+1)),
				}
			},
			code: "markdown_length", path: "$.blocks[0].text",
		},
		{
			name: "total text",
			edit: func(spec *CardSpec) {
				spec.Blocks = []Block{
					PlainText("a", strings.Repeat("x", MaxTotalTextRunes/2+1)),
					PlainText("b", strings.Repeat("y", MaxTotalTextRunes/2+1)),
				}
			},
			code: "total_text_length", path: "$",
		},
		{
			name: "options",
			edit: func(spec *CardSpec) {
				options := make([]SelectOption, MaxSelectOptions+1)
				for index := range options {
					options[index] = SelectOption{
						Label: "Option", Value: "option_" + canonicalNumber(index),
					}
				}
				spec.Blocks = []Block{SingleSelect("choice", InputField{
					FieldID: "choice", FormID: "form", Label: "Choice",
				}, options)}
			},
			code: "select_options_limit", path: "$.blocks[0].options",
		},
		{
			name: "form result",
			edit: func(spec *CardSpec) {
				spec.Blocks = []Block{TextInput("large", InputField{
					FieldID: "large", FormID: "form", Label: "Large",
				}, TextInputConfig{MaxLength: MaxFormResultBytes + 1})}
				spec.Actions[0].FormRef = "form"
			},
			code: "form_result_limit", path: "$.forms.form",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validValidationSpec()
			test.edit(&spec)
			issues := ValidateCardSpec(spec)
			if !hasIssue(issues, test.code, test.path) {
				t.Fatalf("issues = %#v, want %s at %s", issues, test.code, test.path)
			}
		})
	}
}

func TestValidateCardSpecRejectsDuplicateIDsAndBrokenReferences(t *testing.T) {
	spec := validValidationSpec()
	spec.Blocks = append(spec.Blocks, PlainText("body", "duplicate"))
	spec.Blocks = append(spec.Blocks, TextInput("other", InputField{
		FieldID: "reason", FormID: "other_form", Label: "Other",
	}, TextInputConfig{MaxLength: 20}))
	spec.Actions[0].FormRef = "missing_form"
	issues := ValidateCardSpec(spec)
	for _, expected := range []struct {
		code string
		path string
	}{
		{"duplicate_component_id", "$.blocks[2].id"},
		{"duplicate_field_id", "$.blocks[3].field_id"},
		{"unknown_form_ref", "$.actions[0].form_ref"},
	} {
		if !hasIssue(issues, expected.code, expected.path) {
			t.Fatalf("issues = %#v, missing %#v", issues, expected)
		}
	}
}

func TestValidateCardSpecRejectsCyclesInvalidUTF8AndKeepsIssueOrder(t *testing.T) {
	section := &SectionBlock{}
	cyclic := Block{
		Kind: BlockSection, ID: "cycle",
		Section: section,
	}
	section.Blocks = []Block{cyclic}
	spec := CardSpec{
		Version: VersionV1,
		Title:   string([]byte{0xff}),
		Blocks:  []Block{cyclic},
		Actions: []Action{{
			Kind: ActionReset, ID: "reset", Label: "Reset",
			Mode: ActionModeUI, Intent: "reset",
		}},
	}
	first := ValidateCardSpec(spec)
	second := ValidateCardSpec(spec)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("validation issue order is unstable: %#v / %#v", first, second)
	}
	for _, expected := range []struct {
		code string
		path string
	}{
		{"invalid_utf8", "$.title"},
		{"layout_cycle", "$.blocks[0].blocks[0]"},
		{"action_mode_incompatible", "$.actions[0].mode"},
	} {
		if !hasIssue(first, expected.code, expected.path) {
			t.Fatalf("issues = %#v, missing %#v", first, expected)
		}
	}
}

func validValidationSpec() CardSpec {
	return CardSpec{
		Version: VersionV1, Title: "确认修改",
		Blocks: []Block{
			Markdown("body", "确认将会议调整到 10:00？"),
			TextInput("reason", InputField{
				FieldID: "reason", FormID: "schedule_form",
				Label: "修改原因", Required: true, Purpose: "说明日程变更原因",
			}, TextInputConfig{MaxLength: 200}),
		},
		Actions: []Action{{
			Kind: ActionSubmit, ID: "confirm", Label: "确认",
			Style: ActionStylePrimary, Mode: ActionModeCapabilityConfirm,
			Intent: "confirm_schedule_change", FormRef: "schedule_form",
		}},
		Meta: PublicCardMeta{Purpose: "confirmation"},
	}
}

func hasIssue(issues []ValidationIssue, code, path string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Path == path {
			return true
		}
	}
	return false
}

func canonicalNumber(value int) string {
	return strconv.Itoa(value)
}
