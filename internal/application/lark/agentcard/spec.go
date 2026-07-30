package agentcard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const VersionV1 = "agent-card/v1"

type Theme string

const (
	ThemeDefault Theme = ""
	ThemeBlue    Theme = "blue"
	ThemeGreen   Theme = "green"
	ThemeOrange  Theme = "orange"
	ThemeRed     Theme = "red"
	ThemeGrey    Theme = "grey"
)

type CardSpec struct {
	Version string         `json:"version"`
	Title   string         `json:"title"`
	Theme   Theme          `json:"theme,omitempty"`
	Blocks  []Block        `json:"blocks"`
	Actions []Action       `json:"actions,omitempty"`
	Meta    PublicCardMeta `json:"meta,omitempty"`
}

func (s *CardSpec) UnmarshalJSON(data []byte) error {
	type wire CardSpec
	var value wire
	if err := decodeStrictJSON(data, &value); err != nil {
		return fmt.Errorf("decode card spec: %w", err)
	}
	*s = CardSpec(value)
	return nil
}

type PublicCardMeta struct {
	Purpose string   `json:"purpose,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Labels  []string `json:"labels,omitempty"`
	Locale  string   `json:"locale,omitempty"`
}

type BlockKind string

const (
	BlockMarkdown     BlockKind = "markdown"
	BlockPlainText    BlockKind = "plain_text"
	BlockFacts        BlockKind = "facts"
	BlockNote         BlockKind = "note"
	BlockDivider      BlockKind = "divider"
	BlockColumns      BlockKind = "columns"
	BlockSection      BlockKind = "section"
	BlockTextInput    BlockKind = "text_input"
	BlockSingleSelect BlockKind = "single_select"
	BlockMultiSelect  BlockKind = "multi_select"
)

func (k BlockKind) valid() bool {
	switch k {
	case BlockMarkdown, BlockPlainText, BlockFacts, BlockNote, BlockDivider,
		BlockColumns, BlockSection, BlockTextInput, BlockSingleSelect,
		BlockMultiSelect:
		return true
	default:
		return false
	}
}

type Block struct {
	Kind BlockKind `json:"kind"`
	ID   string    `json:"id"`

	Markdown     *MarkdownBlock  `json:"-"`
	PlainText    *PlainTextBlock `json:"-"`
	Facts        *FactsBlock     `json:"-"`
	Note         *NoteBlock      `json:"-"`
	Divider      *DividerBlock   `json:"-"`
	Columns      *ColumnsBlock   `json:"-"`
	Section      *SectionBlock   `json:"-"`
	TextInput    *TextInputBlock `json:"-"`
	SingleSelect *SelectBlock    `json:"-"`
	MultiSelect  *SelectBlock    `json:"-"`
}

type MarkdownBlock struct {
	Text string `json:"text"`
}

type PlainTextBlock struct {
	Text string `json:"text"`
}

type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type FactsBlock struct {
	Items []Fact `json:"items"`
}

type NoteBlock struct {
	Text string `json:"text"`
}

type DividerBlock struct{}

type Column struct {
	ID     string  `json:"id"`
	Width  int     `json:"width,omitempty"`
	Blocks []Block `json:"blocks"`
}

type ColumnsBlock struct {
	Columns []Column `json:"columns"`
}

type SectionBlock struct {
	Title  string  `json:"title,omitempty"`
	Blocks []Block `json:"blocks"`
}

type InputField struct {
	FieldID  string `json:"field_id"`
	Label    string `json:"label"`
	Required bool   `json:"required,omitempty"`
	Purpose  string `json:"purpose,omitempty"`
}

type TextInputConfig struct {
	Placeholder string `json:"placeholder,omitempty"`
	Multiline   bool   `json:"multiline,omitempty"`
	MinLength   int    `json:"min_length,omitempty"`
	MaxLength   int    `json:"max_length,omitempty"`
}

type TextInputBlock struct {
	Field  InputField      `json:"-"`
	Config TextInputConfig `json:"-"`
}

type SelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type SelectBlock struct {
	Field       InputField     `json:"-"`
	Placeholder string         `json:"placeholder,omitempty"`
	Options     []SelectOption `json:"options"`
}

func Markdown(id, text string) Block {
	return Block{
		Kind: BlockMarkdown, ID: id,
		Markdown: &MarkdownBlock{Text: text},
	}
}

func PlainText(id, text string) Block {
	return Block{
		Kind: BlockPlainText, ID: id,
		PlainText: &PlainTextBlock{Text: text},
	}
}

func Facts(id string, items []Fact) Block {
	return Block{
		Kind: BlockFacts, ID: id,
		Facts: &FactsBlock{Items: items},
	}
}

func Note(id, text string) Block {
	return Block{Kind: BlockNote, ID: id, Note: &NoteBlock{Text: text}}
}

func Divider(id string) Block {
	return Block{Kind: BlockDivider, ID: id, Divider: &DividerBlock{}}
}

func Columns(id string, columns []Column) Block {
	return Block{
		Kind: BlockColumns, ID: id,
		Columns: &ColumnsBlock{Columns: columns},
	}
}

func Section(id, title string, blocks []Block) Block {
	return Block{
		Kind: BlockSection, ID: id,
		Section: &SectionBlock{Title: title, Blocks: blocks},
	}
}

func TextInput(id string, field InputField, config TextInputConfig) Block {
	return Block{
		Kind: BlockTextInput, ID: id,
		TextInput: &TextInputBlock{Field: field, Config: config},
	}
}

func SingleSelect(id string, field InputField, options []SelectOption) Block {
	return Block{
		Kind: BlockSingleSelect, ID: id,
		SingleSelect: &SelectBlock{Field: field, Options: options},
	}
}

func MultiSelect(id string, field InputField, options []SelectOption) Block {
	return Block{
		Kind: BlockMultiSelect, ID: id,
		MultiSelect: &SelectBlock{Field: field, Options: options},
	}
}

func (b Block) MarshalJSON() ([]byte, error) {
	if !b.Kind.valid() {
		return nil, fmt.Errorf("unknown card block kind %q", b.Kind)
	}
	switch b.Kind {
	case BlockMarkdown:
		if b.Markdown == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
			*MarkdownBlock
		}{b.Kind, b.ID, b.Markdown})
	case BlockPlainText:
		if b.PlainText == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
			*PlainTextBlock
		}{b.Kind, b.ID, b.PlainText})
	case BlockFacts:
		if b.Facts == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
			*FactsBlock
		}{b.Kind, b.ID, b.Facts})
	case BlockNote:
		if b.Note == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
			*NoteBlock
		}{b.Kind, b.ID, b.Note})
	case BlockDivider:
		if b.Divider == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
		}{b.Kind, b.ID})
	case BlockColumns:
		if b.Columns == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
			*ColumnsBlock
		}{b.Kind, b.ID, b.Columns})
	case BlockSection:
		if b.Section == nil {
			return nil, missingVariant(b)
		}
		return json.Marshal(struct {
			Kind BlockKind `json:"kind"`
			ID   string    `json:"id"`
			*SectionBlock
		}{b.Kind, b.ID, b.Section})
	case BlockTextInput:
		if b.TextInput == nil {
			return nil, missingVariant(b)
		}
		return marshalTextInput(b)
	case BlockSingleSelect:
		if b.SingleSelect == nil {
			return nil, missingVariant(b)
		}
		return marshalSelect(b, b.SingleSelect)
	case BlockMultiSelect:
		if b.MultiSelect == nil {
			return nil, missingVariant(b)
		}
		return marshalSelect(b, b.MultiSelect)
	default:
		panic("validated block kind was not handled")
	}
}

func (b *Block) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Kind BlockKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}
	if !discriminator.Kind.valid() {
		return fmt.Errorf("unknown card block kind %q", discriminator.Kind)
	}
	switch discriminator.Kind {
	case BlockMarkdown:
		var value blockMarkdownWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Block{
			Kind: value.Kind, ID: value.ID,
			Markdown: &MarkdownBlock{Text: value.Text},
		}
	case BlockPlainText:
		var value blockPlainTextWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Block{
			Kind: value.Kind, ID: value.ID,
			PlainText: &PlainTextBlock{Text: value.Text},
		}
	case BlockFacts:
		var value blockFactsWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Block{
			Kind: value.Kind, ID: value.ID,
			Facts: &FactsBlock{Items: value.Items},
		}
	case BlockNote:
		var value blockNoteWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Block{
			Kind: value.Kind, ID: value.ID,
			Note: &NoteBlock{Text: value.Text},
		}
	case BlockDivider:
		var value blockBaseWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Divider(value.ID)
	case BlockColumns:
		var value blockColumnsWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Columns(value.ID, value.Columns)
	case BlockSection:
		var value blockSectionWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = Section(value.ID, value.Title, value.Blocks)
	case BlockTextInput:
		var value blockTextInputWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		*b = TextInput(value.ID, value.InputField, TextInputConfig{
			Placeholder: value.Placeholder, Multiline: value.Multiline,
			MinLength: value.MinLength, MaxLength: value.MaxLength,
		})
	case BlockSingleSelect, BlockMultiSelect:
		var value blockSelectWire
		if err := decodeStrictJSON(data, &value); err != nil {
			return err
		}
		selected := SelectBlock{
			Field: value.InputField, Placeholder: value.Placeholder,
			Options: value.Options,
		}
		*b = Block{Kind: value.Kind, ID: value.ID}
		if value.Kind == BlockSingleSelect {
			b.SingleSelect = &selected
		} else {
			b.MultiSelect = &selected
		}
	}
	return nil
}

type blockBaseWire struct {
	Kind BlockKind `json:"kind"`
	ID   string    `json:"id"`
}

type blockMarkdownWire struct {
	blockBaseWire
	Text string `json:"text"`
}

type blockPlainTextWire struct {
	blockBaseWire
	Text string `json:"text"`
}

type blockFactsWire struct {
	blockBaseWire
	Items []Fact `json:"items"`
}

type blockNoteWire struct {
	blockBaseWire
	Text string `json:"text"`
}

type blockColumnsWire struct {
	blockBaseWire
	Columns []Column `json:"columns"`
}

type blockSectionWire struct {
	blockBaseWire
	Title  string  `json:"title,omitempty"`
	Blocks []Block `json:"blocks"`
}

type blockTextInputWire struct {
	blockBaseWire
	InputField
	Placeholder string `json:"placeholder,omitempty"`
	Multiline   bool   `json:"multiline,omitempty"`
	MinLength   int    `json:"min_length,omitempty"`
	MaxLength   int    `json:"max_length,omitempty"`
}

type blockSelectWire struct {
	blockBaseWire
	InputField
	Placeholder string         `json:"placeholder,omitempty"`
	Options     []SelectOption `json:"options"`
}

func marshalTextInput(b Block) ([]byte, error) {
	value := b.TextInput
	return json.Marshal(blockTextInputWire{
		blockBaseWire: blockBaseWire{Kind: b.Kind, ID: b.ID},
		InputField:    value.Field, Placeholder: value.Config.Placeholder,
		Multiline: value.Config.Multiline, MinLength: value.Config.MinLength,
		MaxLength: value.Config.MaxLength,
	})
}

func marshalSelect(b Block, value *SelectBlock) ([]byte, error) {
	return json.Marshal(blockSelectWire{
		blockBaseWire: blockBaseWire{Kind: b.Kind, ID: b.ID},
		InputField:    value.Field, Placeholder: value.Placeholder,
		Options: value.Options,
	})
}

func missingVariant(block Block) error {
	return fmt.Errorf("card block %q has no %q payload", block.ID, block.Kind)
}

type ActionKind string

const (
	ActionButton ActionKind = "button"
	ActionSubmit ActionKind = "submit"
	ActionReset  ActionKind = "reset"
	ActionCancel ActionKind = "cancel"
)

func (k ActionKind) valid() bool {
	return k == ActionButton || k == ActionSubmit ||
		k == ActionReset || k == ActionCancel
}

type ActionStyle string

const (
	ActionStyleDefault ActionStyle = ""
	ActionStylePrimary ActionStyle = "primary"
	ActionStyleDanger  ActionStyle = "danger"
)

type ActionMode string

const (
	ActionModeUI                ActionMode = "ui_action"
	ActionModeCapabilityConfirm ActionMode = "capability_confirm"
	ActionModeServer            ActionMode = "server_action"
)

type Action struct {
	Kind    ActionKind  `json:"kind"`
	ID      string      `json:"id"`
	Label   string      `json:"label"`
	Style   ActionStyle `json:"style,omitempty"`
	Mode    ActionMode  `json:"mode"`
	Intent  string      `json:"intent"`
	FormRef string      `json:"form_ref,omitempty"`
	URL     string      `json:"url,omitempty"`
}

func (a *Action) UnmarshalJSON(data []byte) error {
	type wire Action
	var value wire
	if err := decodeStrictJSON(data, &value); err != nil {
		return err
	}
	if !value.Kind.valid() {
		return fmt.Errorf("unknown card action kind %q", value.Kind)
	}
	*a = Action(value)
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
