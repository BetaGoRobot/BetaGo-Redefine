package agentcardcompiler

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
)

func TestCompilerProducesDeterministicSchemaV2AndRuntimeEnvelope(t *testing.T) {
	bound := compilerFixture(t, agentcard.LifecycleInteractive)
	compiler := New()
	first, err := compiler.Compile(bound)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	second, err := compiler.Compile(bound)
	if err != nil {
		t.Fatalf("Compile(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("compiler output is not deterministic")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	for _, required := range []string{
		`"schema":"2.0"`,
		`"tag":"markdown"`,
		`"tag":"input"`,
		`"tag":"select_static"`,
		`"tag":"multi_select_static"`,
		`"action":"agent.runtime.resume"`,
		`"token":"plaintext-token"`,
		`"form_action_type":"submit"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("compiled card missing %s: %s", required, text)
		}
	}

	redacted, err := compiler.CompileRedacted(bound)
	if err != nil {
		t.Fatalf("CompileRedacted() error = %v", err)
	}
	redactedJSON, _ := json.Marshal(redacted)
	if strings.Contains(string(redactedJSON), "plaintext-token") ||
		!strings.Contains(string(redactedJSON), "[REDACTED]") {
		t.Fatalf("redacted compiled card = %s", redactedJSON)
	}
}

func TestCompilerLifecycleSurfacesRemoveActions(t *testing.T) {
	for _, state := range []agentcard.LifecycleState{
		agentcard.LifecycleSubmitted,
		agentcard.LifecycleProcessing,
		agentcard.LifecycleResolved,
		agentcard.LifecycleExpired,
		agentcard.LifecycleFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			card, err := New().Compile(compilerFixture(t, state))
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			encoded, _ := json.Marshal(card)
			if strings.Contains(string(encoded), "plaintext-token") ||
				strings.Contains(string(encoded), `"tag":"button"`) {
				t.Fatalf("%s surface remained interactive: %s", state, encoded)
			}
		})
	}
}

func TestCompilerRejectsUnboundCallbackAction(t *testing.T) {
	spec := compilerSpec()
	bound, err := agentcard.NewBoundCardSpec(
		spec,
		agentcard.LifecycleInteractive,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBoundCardSpec() error = %v", err)
	}
	if _, err := New().Compile(bound); err == nil {
		t.Fatal("Compile() accepted unbound callback action")
	}
}

func compilerFixture(
	t *testing.T,
	state agentcard.LifecycleState,
) *agentcard.BoundCardSpec {
	t.Helper()
	spec := compilerSpec()
	bound, err := agentcard.NewBoundCardSpec(
		spec,
		state,
		map[string]agentcard.RuntimeBinding{
			"confirm": {
				RunID: "run-1", StepID: "step-1", InteractionID: "interaction-1",
				Revision: 3, Token: "plaintext-token",
				InteractionKind: "ui_action",
			},
		},
	)
	if err != nil {
		t.Fatalf("NewBoundCardSpec() error = %v", err)
	}
	return bound
}

func compilerSpec() agentcard.CardSpec {
	return agentcard.CardSpec{
		Version: agentcard.VersionV1, Title: "确认信息",
		Theme: agentcard.ThemeBlue,
		Blocks: []agentcard.Block{
			agentcard.Markdown("body", "**请确认**以下信息"),
			agentcard.Facts("facts", []agentcard.Fact{{Label: "时间", Value: "10:00"}}),
			agentcard.Note("note", "提交后进入处理"),
			agentcard.Divider("divider"),
			agentcard.Columns("columns", []agentcard.Column{
				{ID: "left", Blocks: []agentcard.Block{agentcard.PlainText("left_text", "左")}},
				{ID: "right", Blocks: []agentcard.Block{agentcard.PlainText("right_text", "右")}},
			}),
			agentcard.TextInput("reason", agentcard.InputField{
				FieldID: "reason", FormID: "form", Label: "原因", Required: true,
			}, agentcard.TextInputConfig{Placeholder: "请输入", MaxLength: 100}),
			agentcard.SingleSelect("single", agentcard.InputField{
				FieldID: "single", FormID: "form", Label: "单选",
			}, []agentcard.SelectOption{{Label: "A", Value: "a"}}),
			agentcard.MultiSelect("multi", agentcard.InputField{
				FieldID: "multi", FormID: "form", Label: "多选",
			}, []agentcard.SelectOption{{Label: "A", Value: "a"}}),
		},
		Actions: []agentcard.Action{{
			Kind: agentcard.ActionSubmit, ID: "confirm", Label: "确认",
			Style: agentcard.ActionStylePrimary, Mode: agentcard.ActionModeUI,
			Intent: "confirm", FormRef: "form",
		}},
	}
}
