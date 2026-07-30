package agentcard

import (
	"fmt"
	"regexp"
	"sort"
	"unicode/utf8"
)

const (
	MaxBlocks          = 20
	MaxInputs          = 10
	MaxActions         = 5
	MaxColumns         = 3
	MaxNestingDepth    = 3
	MaxTitleRunes      = 80
	MaxMarkdownRunes   = 4000
	MaxTotalTextRunes  = 12000
	MaxSelectOptions   = 50
	MaxFormResultBytes = 8 * 1024
)

type ValidationIssue struct {
	Code   string `json:"code"`
	Path   string `json:"path"`
	Limit  int    `json:"limit,omitempty"`
	Actual int    `json:"actual,omitempty"`
}

var canonicalIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type validationState struct {
	issues []ValidationIssue

	blockCount int
	inputCount int
	totalText  int

	componentIDs map[string]string
	fieldIDs     map[string]string
	actionIDs    map[string]string
	forms        map[string]int

	activeSections map[*SectionBlock]bool
	activeColumns  map[*ColumnsBlock]bool
}

func ValidateCardSpec(spec CardSpec) []ValidationIssue {
	state := &validationState{
		componentIDs:   make(map[string]string),
		fieldIDs:       make(map[string]string),
		actionIDs:      make(map[string]string),
		forms:          make(map[string]int),
		activeSections: make(map[*SectionBlock]bool),
		activeColumns:  make(map[*ColumnsBlock]bool),
	}
	if spec.Version != VersionV1 {
		state.add("unsupported_version", "$.version", 0, 0)
	}
	if !validTheme(spec.Theme) {
		state.add("invalid_theme", "$.theme", 0, 0)
	}
	state.text("$.title", spec.Title, MaxTitleRunes, "title_length")
	for index := range spec.Blocks {
		state.visitBlock(
			spec.Blocks[index],
			fmt.Sprintf("$.blocks[%d]", index),
			1,
		)
	}
	if state.blockCount > MaxBlocks {
		state.add("blocks_limit", "$.blocks", MaxBlocks, state.blockCount)
	}
	if state.inputCount > MaxInputs {
		state.add("inputs_limit", "$.blocks", MaxInputs, state.inputCount)
	}
	if len(spec.Actions) > MaxActions {
		state.add("actions_limit", "$.actions", MaxActions, len(spec.Actions))
	}
	for index := range spec.Actions {
		state.visitAction(spec.Actions[index], fmt.Sprintf("$.actions[%d]", index))
	}
	for formID, estimatedBytes := range state.forms {
		if estimatedBytes > MaxFormResultBytes {
			state.add(
				"form_result_limit",
				"$.forms."+formID,
				MaxFormResultBytes,
				estimatedBytes,
			)
		}
	}
	if state.totalText > MaxTotalTextRunes {
		state.add(
			"total_text_length",
			"$",
			MaxTotalTextRunes,
			state.totalText,
		)
	}
	sort.SliceStable(state.issues, func(i, j int) bool {
		if state.issues[i].Path == state.issues[j].Path {
			return state.issues[i].Code < state.issues[j].Code
		}
		return state.issues[i].Path < state.issues[j].Path
	})
	return state.issues
}

func (s *validationState) visitBlock(block Block, path string, depth int) {
	s.blockCount++
	if depth > MaxNestingDepth {
		s.add("nesting_depth", path, MaxNestingDepth, depth)
	}
	s.uniqueID(block.ID, path+".id", s.componentIDs, "duplicate_component_id")
	if !block.Kind.valid() {
		s.add("unknown_block_kind", path+".kind", 0, 0)
		return
	}
	if payloadCount(block) != 1 || !matchingPayload(block) {
		s.add("block_payload", path, 1, payloadCount(block))
		return
	}
	switch block.Kind {
	case BlockMarkdown:
		s.text(path+".text", block.Markdown.Text, MaxMarkdownRunes, "markdown_length")
	case BlockPlainText:
		s.text(path+".text", block.PlainText.Text, 0, "")
	case BlockFacts:
		for index, item := range block.Facts.Items {
			s.text(fmt.Sprintf("%s.items[%d].label", path, index), item.Label, 0, "")
			s.text(fmt.Sprintf("%s.items[%d].value", path, index), item.Value, 0, "")
		}
	case BlockNote:
		s.text(path+".text", block.Note.Text, 0, "")
	case BlockDivider:
	case BlockColumns:
		if s.activeColumns[block.Columns] {
			s.add("layout_cycle", path, 0, 0)
			return
		}
		s.activeColumns[block.Columns] = true
		if len(block.Columns.Columns) > MaxColumns {
			s.add(
				"columns_limit",
				path+".columns",
				MaxColumns,
				len(block.Columns.Columns),
			)
		}
		for columnIndex, column := range block.Columns.Columns {
			columnPath := fmt.Sprintf("%s.columns[%d]", path, columnIndex)
			s.uniqueID(
				column.ID,
				columnPath+".id",
				s.componentIDs,
				"duplicate_component_id",
			)
			for index := range column.Blocks {
				s.visitBlock(
					column.Blocks[index],
					fmt.Sprintf("%s.blocks[%d]", columnPath, index),
					depth+1,
				)
			}
		}
		delete(s.activeColumns, block.Columns)
	case BlockSection:
		if s.activeSections[block.Section] {
			s.add("layout_cycle", path, 0, 0)
			return
		}
		s.activeSections[block.Section] = true
		s.text(path+".title", block.Section.Title, 0, "")
		for index := range block.Section.Blocks {
			s.visitBlock(
				block.Section.Blocks[index],
				fmt.Sprintf("%s.blocks[%d]", path, index),
				depth+1,
			)
		}
		delete(s.activeSections, block.Section)
	case BlockTextInput:
		s.visitInput(
			block.TextInput.Field,
			path,
			textInputEstimate(block.TextInput.Config),
		)
		if block.TextInput.Config.MinLength < 0 ||
			block.TextInput.Config.MaxLength < 0 ||
			(block.TextInput.Config.MaxLength > 0 &&
				block.TextInput.Config.MinLength > block.TextInput.Config.MaxLength) {
			s.add("invalid_input_length", path, 0, 0)
		}
		s.text(path+".placeholder", block.TextInput.Config.Placeholder, 0, "")
	case BlockSingleSelect:
		s.visitSelect(block.SingleSelect, path, false)
	case BlockMultiSelect:
		s.visitSelect(block.MultiSelect, path, true)
	}
}

func (s *validationState) visitInput(field InputField, path string, estimate int) {
	s.inputCount++
	s.uniqueID(field.FieldID, path+".field_id", s.fieldIDs, "duplicate_field_id")
	if !canonicalIDPattern.MatchString(field.FormID) {
		s.add("invalid_form_id", path+".form_id", 0, 0)
	} else {
		s.forms[field.FormID] += estimate
	}
	s.text(path+".label", field.Label, 0, "")
	s.text(path+".purpose", field.Purpose, 0, "")
	if field.Label == "" {
		s.add("required_label", path+".label", 0, 0)
	}
}

func (s *validationState) visitSelect(value *SelectBlock, path string, multi bool) {
	estimate := 0
	if len(value.Options) > MaxSelectOptions {
		s.add(
			"select_options_limit",
			path+".options",
			MaxSelectOptions,
			len(value.Options),
		)
	}
	seen := make(map[string]struct{}, len(value.Options))
	maximum := 0
	for index, option := range value.Options {
		optionPath := fmt.Sprintf("%s.options[%d]", path, index)
		s.text(optionPath+".label", option.Label, 0, "")
		s.text(optionPath+".value", option.Value, 0, "")
		size := len(option.Value)
		if _, exists := seen[option.Value]; exists {
			s.add("duplicate_option_value", optionPath+".value", 0, 0)
		}
		seen[option.Value] = struct{}{}
		if multi {
			estimate += size
		} else if size > maximum {
			maximum = size
		}
	}
	if !multi {
		estimate = maximum
	}
	s.visitInput(value.Field, path, estimate)
	s.text(path+".placeholder", value.Placeholder, 0, "")
}

func (s *validationState) visitAction(action Action, path string) {
	s.uniqueID(action.ID, path+".id", s.actionIDs, "duplicate_action_id")
	s.text(path+".label", action.Label, 0, "")
	s.text(path+".intent", action.Intent, 0, "")
	if !action.Kind.valid() {
		s.add("unknown_action_kind", path+".kind", 0, 0)
	}
	if action.Style != ActionStyleDefault &&
		action.Style != ActionStylePrimary &&
		action.Style != ActionStyleDanger {
		s.add("invalid_action_style", path+".style", 0, 0)
	}
	if action.Mode != ActionModeUI &&
		action.Mode != ActionModeCapabilityConfirm &&
		action.Mode != ActionModeServer {
		s.add("invalid_action_mode", path+".mode", 0, 0)
	}
	if action.Mode == ActionModeCapabilityConfirm &&
		action.Kind != ActionButton && action.Kind != ActionSubmit {
		s.add("action_mode_incompatible", path+".mode", 0, 0)
	}
	if action.Kind == ActionReset && action.Mode != ActionModeServer {
		s.add("action_mode_incompatible", path+".mode", 0, 0)
	}
	if action.FormRef != "" {
		if !canonicalIDPattern.MatchString(action.FormRef) {
			s.add("invalid_form_ref", path+".form_ref", 0, 0)
		} else if _, exists := s.forms[action.FormRef]; !exists {
			s.add("unknown_form_ref", path+".form_ref", 0, 0)
		}
	}
}

func (s *validationState) uniqueID(
	value string,
	path string,
	seen map[string]string,
	duplicateCode string,
) {
	if !canonicalIDPattern.MatchString(value) {
		s.add("invalid_id", path, 0, 0)
		return
	}
	if _, exists := seen[value]; exists {
		s.add(duplicateCode, path, 0, 0)
		return
	}
	seen[value] = path
}

func (s *validationState) text(
	path string,
	value string,
	limit int,
	code string,
) {
	if !utf8.ValidString(value) {
		s.add("invalid_utf8", path, 0, 0)
		return
	}
	length := utf8.RuneCountInString(value)
	s.totalText += length
	if limit > 0 && length > limit {
		s.add(code, path, limit, length)
	}
}

func (s *validationState) add(code, path string, limit, actual int) {
	s.issues = append(s.issues, ValidationIssue{
		Code: code, Path: path, Limit: limit, Actual: actual,
	})
}

func validTheme(theme Theme) bool {
	switch theme {
	case ThemeDefault, ThemeBlue, ThemeGreen, ThemeOrange, ThemeRed, ThemeGrey:
		return true
	default:
		return false
	}
}

func payloadCount(block Block) int {
	count := 0
	for _, present := range []bool{
		block.Markdown != nil,
		block.PlainText != nil,
		block.Facts != nil,
		block.Note != nil,
		block.Divider != nil,
		block.Columns != nil,
		block.Section != nil,
		block.TextInput != nil,
		block.SingleSelect != nil,
		block.MultiSelect != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func matchingPayload(block Block) bool {
	switch block.Kind {
	case BlockMarkdown:
		return block.Markdown != nil
	case BlockPlainText:
		return block.PlainText != nil
	case BlockFacts:
		return block.Facts != nil
	case BlockNote:
		return block.Note != nil
	case BlockDivider:
		return block.Divider != nil
	case BlockColumns:
		return block.Columns != nil
	case BlockSection:
		return block.Section != nil
	case BlockTextInput:
		return block.TextInput != nil
	case BlockSingleSelect:
		return block.SingleSelect != nil
	case BlockMultiSelect:
		return block.MultiSelect != nil
	default:
		return false
	}
}

func textInputEstimate(config TextInputConfig) int {
	if config.MaxLength > 0 {
		return config.MaxLength
	}
	return 1024
}
