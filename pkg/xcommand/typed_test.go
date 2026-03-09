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
	Scope string `json:"scope"`
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
	if specs[2].Name != "force" || !specs[2].Flag {
		t.Fatalf("unexpected third arg spec: %+v", specs[2])
	}
	if specs[3].Name != "query" || !specs[3].Input {
		t.Fatalf("unexpected fourth arg spec: %+v", specs[3])
	}

	examples := cmd.GetExamples()
	if len(examples) != 1 || examples[0] != "doc --name=alice" {
		t.Fatalf("unexpected examples: %+v", examples)
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
	if metaData.UserID != "user-id" {
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

func TestBindTool(t *testing.T) {
	handler := BindTool(toolTestHandler{})

	value, err := handler(context.Background(), `{"name":"bob"}`, arktools.FCMeta[toolTestMeta]{
		ChatID: "chat-id",
		UserID: "user-id",
		Data:   &toolTestMeta{ID: "payload-id"},
	}).Get()
	if err != nil {
		t.Fatalf("tool handler returned error: %v", err)
	}
	if value != "bob" {
		t.Fatalf("unexpected tool result: %s", value)
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
