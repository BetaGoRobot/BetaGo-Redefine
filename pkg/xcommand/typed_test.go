package xcommand

import (
	"context"
	"errors"
	"strings"
	"testing"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

type cliTestArgs struct {
	Name string `json:"name" cli:"name,input,required" help:"用户名"`
}

type cliTestHandler struct{}

func (cliTestHandler) ParseCLI(raw []string) (cliTestArgs, error) {
	if len(raw) == 0 {
		return cliTestArgs{}, errors.New("missing args")
	}
	return cliTestArgs{Name: raw[0]}, nil
}

func (cliTestHandler) Handle(ctx context.Context, data string, metaData *xhandler.BaseMetaData, arg cliTestArgs) error {
	if data != "payload" {
		return errors.New("unexpected data")
	}
	metaData.SetExtra("name", arg.Name)
	return nil
}

func TestBindCLI(t *testing.T) {
	metaData := &xhandler.BaseMetaData{}
	err := BindCLI[string](cliTestHandler{})(context.Background(), "payload", metaData, "alice")
	if err != nil {
		t.Fatalf("command func returned error: %v", err)
	}

	val, ok := metaData.GetExtra("name")
	if !ok {
		t.Fatal("expected name in metadata")
	}
	if val != "alice" {
		t.Fatalf("unexpected metadata value: %s", val)
	}
}

func TestRedirectHelpArgs(t *testing.T) {
	root := NewRootCommand[string](nil)

	root.AddSubCommand(NewCommand[string]("help", nil))
	root.AddSubCommand(NewCommand[string]("doc", nil))
	root.BuildChain()

	got, ok := root.redirectHelpArgs([]string{"doc", "--help"})
	if !ok {
		t.Fatal("expected inline help redirect")
	}
	if strings.Join(got, " ") != "doc" {
		t.Fatalf("unexpected help args: %+v", got)
	}

	got, ok = root.redirectHelpArgs([]string{"bb", "--r", "--help"})
	if !ok {
		t.Fatal("expected inline help redirect with flags")
	}
	if strings.Join(got, " ") != "bb" {
		t.Fatalf("unexpected help args with flags: %+v", got)
	}

	if _, ok = root.redirectHelpArgs([]string{"help", "doc"}); ok {
		t.Fatal("did not expect direct help command to redirect")
	}
}

type docArgs struct {
	Name  string `json:"name" cli:"name,required" help:"用户名"`
	Scope string `json:"scope" enum:"chat:群聊,user:用户,global:全局"`
	Force bool   `json:"force" cli:"force,flag"`
	Input string `json:"input" cli:"query,input"`
}

type docHandler struct{}

func (docHandler) ParseCLI(raw []string) (docArgs, error) {
	return docArgs{}, nil
}

func (docHandler) Handle(ctx context.Context, data string, metaData *xhandler.BaseMetaData, arg docArgs) error {
	return nil
}

func (docHandler) CommandDescription() string {
	return "doc command"
}

func (docHandler) CommandExamples() []string {
	return []string{"doc --name=alice"}
}

func (docHandler) ToolSpec() ToolSpec {
	return ToolSpec{
		Name: "doc.tool",
		Desc: "doc tool",
		Params: arktools.NewParams("object").
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "作用域",
			}).
			AddRequired("scope"),
	}
}

func TestNewTypedCommandAutoArgs(t *testing.T) {
	cmd := NewTypedCommand[string, docArgs]("doc", docHandler{})

	got := cmd.FormatUsage()
	if cmd.Description != "doc command" {
		t.Fatalf("expected command description override, got: %s", cmd.Description)
	}
	if !strings.Contains(got, "--name=<value>") {
		t.Fatalf("expected required named arg in usage, got: %s", got)
	}
	if !strings.Contains(got, "[--scope=<value>]") && !strings.Contains(got, "--scope=<value>") {
		t.Fatalf("expected scope arg in usage, got: %s", got)
	}
	if !strings.Contains(got, "[--force]") {
		t.Fatalf("expected flag arg in usage, got: %s", got)
	}
	if !strings.Contains(got, "[query]") {
		t.Fatalf("expected positional arg in usage, got: %s", got)
	}
	if !strings.Contains(got, "用户名") {
		t.Fatalf("expected help text in usage, got: %s", got)
	}
	if !strings.Contains(got, "作用域") {
		t.Fatalf("expected tool spec desc fallback in usage, got: %s", got)
	}

	specs := cmd.GetArgSpecs()
	if len(specs) != 4 {
		t.Fatalf("expected 4 arg specs, got: %d", len(specs))
	}
	if specs[0].Name != "name" || !specs[0].Required {
		t.Fatalf("unexpected first arg spec: %+v", specs[0])
	}
	if specs[1].Name != "scope" || specs[1].Description != "作用域" {
		t.Fatalf("unexpected second arg spec: %+v", specs[1])
	}
	if len(specs[1].Options) != 3 || specs[1].Options[0].Value != "chat" || specs[1].Options[0].Label != "群聊" {
		t.Fatalf("expected enum options on scope arg, got: %+v", specs[1].Options)
	}
	if specs[2].Name != "force" || !specs[2].Flag {
		t.Fatalf("unexpected third arg spec: %+v", specs[2])
	}
	if len(specs[2].Options) != 2 || specs[2].Options[0].Value != "true" || specs[2].Options[1].Value != "false" {
		t.Fatalf("expected bool options on force arg, got: %+v", specs[2].Options)
	}
	if specs[3].Name != "query" || !specs[3].Input {
		t.Fatalf("unexpected fourth arg spec: %+v", specs[3])
	}

	examples := cmd.GetExamples()
	if len(examples) != 1 || examples[0] != "doc --name=alice" {
		t.Fatalf("unexpected examples: %+v", examples)
	}
}

type typedScope string

const (
	typedScopeChat   typedScope = "chat"
	typedScopeUser   typedScope = "user"
	typedScopeGlobal typedScope = "global"
)

func (typedScope) CommandEnum() EnumDescriptor {
	return EnumDescriptor{
		Options: []CommandArgOption{
			{Value: string(typedScopeChat), Label: "群聊"},
			{Value: string(typedScopeUser), Label: "用户"},
			{Value: string(typedScopeGlobal), Label: "全局"},
		},
		DefaultValue: string(typedScopeChat),
	}
}

type typedEnumArgs struct {
	Scope  typedScope `json:"scope"`
	Legacy string     `json:"legacy" enum:"x:X,y:Y"`
}

type typedEnumHandler struct{}

func (typedEnumHandler) ParseCLI(raw []string) (typedEnumArgs, error) {
	return typedEnumArgs{}, nil
}

func (typedEnumHandler) Handle(ctx context.Context, data string, metaData *xhandler.BaseMetaData, arg typedEnumArgs) error {
	return nil
}

func TestNewTypedCommandUsesTypedEnumDescriptor(t *testing.T) {
	cmd := NewTypedCommand[string, typedEnumArgs]("typed", typedEnumHandler{})
	specs := cmd.GetArgSpecs()
	if len(specs) != 2 {
		t.Fatalf("expected 2 arg specs, got: %d", len(specs))
	}
	if specs[0].Name != "scope" {
		t.Fatalf("unexpected first arg spec: %+v", specs[0])
	}
	if specs[0].DefaultValue != "chat" {
		t.Fatalf("expected typed enum default value, got: %+v", specs[0])
	}
	if len(specs[0].Options) != 3 || specs[0].Options[1].Value != "user" {
		t.Fatalf("expected typed enum options, got: %+v", specs[0].Options)
	}
	if specs[1].Name != "legacy" || len(specs[1].Options) != 2 || specs[1].DefaultValue != "" {
		t.Fatalf("expected legacy enum tag fallback, got: %+v", specs[1])
	}
}

var dynamicEnumOptions = []CommandArgOption{
	{Value: "first", Label: "First"},
}

type dynamicEnum string

func (dynamicEnum) CommandEnum() EnumDescriptor {
	return EnumDescriptor{
		Options:      append([]CommandArgOption(nil), dynamicEnumOptions...),
		DefaultValue: dynamicEnumOptions[0].Value,
	}
}

type dynamicEnumArgs struct {
	Value dynamicEnum `json:"value"`
}

type dynamicEnumHandler struct{}

func (dynamicEnumHandler) ParseCLI(raw []string) (dynamicEnumArgs, error) {
	return dynamicEnumArgs{}, nil
}

func (dynamicEnumHandler) Handle(ctx context.Context, data string, metaData *xhandler.BaseMetaData, arg dynamicEnumArgs) error {
	return nil
}

func TestNewTypedCommandResolvesEnumDescriptorLazily(t *testing.T) {
	dynamicEnumOptions = []CommandArgOption{
		{Value: "first", Label: "First"},
	}
	cmd := NewTypedCommand[string, dynamicEnumArgs]("dynamic", dynamicEnumHandler{})

	dynamicEnumOptions = []CommandArgOption{
		{Value: "second", Label: "Second"},
		{Value: "third", Label: "Third"},
	}

	specs := cmd.GetArgSpecs()
	if len(specs) != 1 {
		t.Fatalf("expected 1 arg spec, got: %d", len(specs))
	}
	if specs[0].DefaultValue != "second" {
		t.Fatalf("expected lazy default value, got: %+v", specs[0])
	}
	if len(specs[0].Options) != 2 || specs[0].Options[0].Value != "second" || specs[0].Options[1].Value != "third" {
		t.Fatalf("expected lazy enum options, got: %+v", specs[0].Options)
	}
}

func TestParseEnumUsesDefaultAndRejectsInvalidValue(t *testing.T) {
	got, err := ParseEnum[typedScope]("")
	if err != nil {
		t.Fatalf("ParseEnum() default error = %v", err)
	}
	if got != typedScopeChat {
		t.Fatalf("expected default enum value, got: %q", got)
	}

	got, err = ParseEnum[typedScope]("user")
	if err != nil {
		t.Fatalf("ParseEnum() explicit value error = %v", err)
	}
	if got != typedScopeUser {
		t.Fatalf("unexpected enum value: %q", got)
	}

	if _, err := ParseEnum[typedScope]("team"); err == nil {
		t.Fatal("expected invalid enum value to fail")
	}
}

type toolTestMeta struct {
	ID string
}

type toolTestArgs struct {
	Name string
}

type toolTestHandler struct{}

func (toolTestHandler) ParseTool(raw string) (toolTestArgs, error) {
	if raw != `{"name":"bob"}` {
		return toolTestArgs{}, errors.New("unexpected payload")
	}
	return toolTestArgs{Name: "bob"}, nil
}

func (toolTestHandler) Handle(ctx context.Context, data *toolTestMeta, metaData *xhandler.BaseMetaData, arg toolTestArgs) error {
	if metaData.ChatID != "chat-id" {
		return errors.New("unexpected chat id")
	}
	if metaData.OpenID != "user-id" {
		return errors.New("unexpected user id")
	}
	if data.ID != "payload-id" {
		return errors.New("unexpected data id")
	}
	metaData.SetExtra("result", arg.Name)
	return nil
}

func (toolTestHandler) ToolSpec() ToolSpec {
	return ToolSpec{
		Name: "test.tool",
		Desc: "test tool",
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("result")
			return result
		},
	}
}

type toolEnumArgs struct {
	Scope  typedScope `json:"scope"`
	Notify bool       `json:"notify"`
}

type toolEnumHandler struct{}

func (toolEnumHandler) ParseTool(raw string) (toolEnumArgs, error) {
	return toolEnumArgs{}, nil
}

func (toolEnumHandler) Handle(ctx context.Context, data *toolTestMeta, metaData *xhandler.BaseMetaData, arg toolEnumArgs) error {
	return nil
}

func (toolEnumHandler) ToolSpec() ToolSpec {
	return ToolSpec{
		Name: "test.enum",
		Desc: "test enum tool",
		Params: arktools.NewParams("object").
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "作用域",
			}).
			AddProp("notify", &arktools.Prop{
				Type: "boolean",
				Desc: "是否通知",
			}),
	}
}

func TestBindTool(t *testing.T) {
	handler := BindTool(toolTestHandler{})

	value, err := handler(context.Background(), `{"name":"bob"}`, arktools.FCMeta[toolTestMeta]{
		ChatID: "chat-id",
		OpenID: "user-id",
		Data:   &toolTestMeta{ID: "payload-id"},
	}).Get()
	if err != nil {
		t.Fatalf("tool handler returned error: %v", err)
	}
	if value != "bob" {
		t.Fatalf("unexpected tool result: %s", value)
	}
}

func TestExecuteUsesDefaultSubCommand(t *testing.T) {
	root := NewRootCommand[string](nil)
	meta := &xhandler.BaseMetaData{}
	called := false

	root.AddSubCommand(
		NewCommand[string]("schedule", nil).
			SetDefaultSubCommand("manage").
			AddSubCommand(NewCommand[string]("manage", func(ctx context.Context, data string, metaData *xhandler.BaseMetaData, args ...string) error {
				called = true
				if data != "payload" {
					t.Fatalf("unexpected data: %s", data)
				}
				if len(args) != 0 {
					t.Fatalf("unexpected args: %+v", args)
				}
				metaData.SetExtra("ok", "1")
				return nil
			})),
	)
	root.BuildChain()

	if err := root.Execute(context.Background(), "payload", meta, []string{"schedule"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("expected default sub command to be called")
	}
	value, ok := meta.GetExtra("ok")
	if !ok || value != "1" {
		t.Fatalf("unexpected meta: %v %v", value, ok)
	}
}

func TestFindAndExecuteResolveAliases(t *testing.T) {
	root := NewRootCommand[string](nil)
	meta := &xhandler.BaseMetaData{}
	called := false

	root.AddSubCommand(
		NewCommand[string]("wordcount", nil).
			AddAliases("wc").
			SetDefaultSubCommand("summary").
			AddSubCommand(
				NewCommand[string]("summary", func(ctx context.Context, data string, metaData *xhandler.BaseMetaData, args ...string) error {
					called = true
					metaData.SetExtra("path", "summary")
					return nil
				}),
			).
			AddSubCommand(
				NewCommand[string]("chunks", func(ctx context.Context, data string, metaData *xhandler.BaseMetaData, args ...string) error {
					metaData.SetExtra("path", "chunks")
					return nil
				}),
			),
	)
	root.BuildChain()

	if target := root.Find("wc", "chunks"); target == nil || target.Path() != "/wordcount chunks" {
		t.Fatalf("expected alias path to resolve to canonical chunks command, got: %+v", target)
	}
	if !root.IsCommand(context.Background(), "/wc") {
		t.Fatal("expected alias to be treated as command")
	}
	if err := root.Execute(context.Background(), "payload", meta, []string{"wc"}); err != nil {
		t.Fatalf("Execute() via alias error = %v", err)
	}
	if !called {
		t.Fatal("expected alias execution to hit default subcommand")
	}
	if value, ok := meta.GetExtra("path"); !ok || value != "summary" {
		t.Fatalf("expected alias execution to resolve to summary, got: %v %v", value, ok)
	}
}

func TestRegisterTool(t *testing.T) {
	ins := arktools.New[toolTestMeta]()

	RegisterTool(ins, toolTestHandler{})

	unit, ok := ins.Get("test.tool")
	if !ok {
		t.Fatal("expected registered tool")
	}
	if unit.Description != "test tool" {
		t.Fatalf("unexpected description: %s", unit.Description)
	}
	if unit.Parameters == nil {
		t.Fatal("expected default params")
	}
	if unit.Parameters.Type != "object" {
		t.Fatalf("unexpected params type: %s", unit.Parameters.Type)
	}
}

func TestRegisterToolInfersEnumAndDefaultFromTypedArgs(t *testing.T) {
	ins := arktools.New[toolTestMeta]()

	RegisterTool(ins, toolEnumHandler{})

	unit, ok := ins.Get("test.enum")
	if !ok {
		t.Fatal("expected registered tool")
	}

	scopeProp := unit.Parameters.Props["scope"]
	if scopeProp == nil {
		t.Fatal("expected scope prop")
	}
	if len(scopeProp.Enum) != 3 || scopeProp.Enum[0] != "chat" || scopeProp.Enum[2] != "global" {
		t.Fatalf("unexpected scope enum: %+v", scopeProp.Enum)
	}
	if scopeProp.Default != "chat" {
		t.Fatalf("unexpected scope default: %#v", scopeProp.Default)
	}

	notifyProp := unit.Parameters.Props["notify"]
	if notifyProp == nil {
		t.Fatal("expected notify prop")
	}
	if len(notifyProp.Enum) != 2 || notifyProp.Enum[0] != true || notifyProp.Enum[1] != false {
		t.Fatalf("unexpected notify enum: %+v", notifyProp.Enum)
	}
	if notifyProp.Default != false {
		t.Fatalf("unexpected notify default: %#v", notifyProp.Default)
	}
}
