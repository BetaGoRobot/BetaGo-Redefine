package mememodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestCompletionCarriesOnlyStageInputAndAccountsActualUsage(t *testing.T) {
	var captured *responses.ResponsesRequest
	model, err := New(Options{ModelID: "stage-model", Call: func(_ context.Context, req *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
		captured = req
		var out responses.ResponseObject
		if err := json.Unmarshal([]byte(`{"id":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"relations\":[],\"emotion_chain\":[],\"keys\":[]}"}]}],"usage":{"input_tokens":21,"output_tokens":12,"total_tokens":33}}`), &out); err != nil {
			t.Fatal(err)
		}
		return &out, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageQuery, SystemPrompt: "stage-only", InputJSON: json.RawMessage(`{"request_id":"fixture"}`)})
	if err != nil || !json.Valid(result.JSON) || result.Metadata.Model != "stage-model" || result.Metadata.InputTokens != 21 || result.Metadata.OutputTokens != 12 {
		t.Fatalf("completion: %+v %v", result, err)
	}
	if captured.Model != "stage-model" || len(captured.Tools) != 0 || captured.GetText().GetFormat().GetType() != responses.TextType_json_object {
		t.Fatalf("unexpected request mode: %+v", captured)
	}
}

func TestStageSchemaIsSentAsJSONSchemaOnActualSDKWire(t *testing.T) {
	for _, stage := range []memes.Stage{memes.StageQuery, memes.StageEncode} {
		t.Run(string(stage), func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
			var captured *responses.ResponsesRequest
			m, _ := New(Options{ModelID: "fixture", MaxOutputTokens: 8192, Call: func(_ context.Context, request *responses.ResponsesRequest, _ llmusage.Scope) (*responses.ResponseObject, error) {
				captured = request
				return responseFixture(t, `{"ok":true}`, 5), nil
			}})
			if _, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: stage, SystemPrompt: "JSON", InputJSON: json.RawMessage(`{}`), Schema: schema}); err != nil {
				t.Fatal(err)
			}
			format := captured.GetText().GetFormat()
			if format.GetType() != responses.TextType_json_schema || format.GetName() != "meme_"+string(stage) || format.Strict == nil || format.GetStrict() {
				t.Fatal("schema format was only a system prompt rather than request parameters")
			}
			wire, err := json.Marshal(captured)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Text struct {
					Format struct {
						Type   string          `json:"type"`
						Name   string          `json:"name"`
						Strict *bool           `json:"strict"`
						Schema json.RawMessage `json:"schema"`
					} `json:"format"`
				} `json:"text"`
			}
			if err = json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			actual := decoded.Text.Format
			if actual.Type != "json_schema" || actual.Name != "meme_"+string(stage) || actual.Strict == nil || *actual.Strict || string(actual.Schema) != string(schema) {
				t.Fatalf("SDK wire schema missing/encoded incorrectly: %+v", actual)
			}
			if captured.MaxOutputTokens == nil || *captured.MaxOutputTokens != 8192 {
				t.Fatal("schema mode changed configured output budget")
			}
			schema[0] = '['
			if captured.GetText().GetFormat().GetSchema().GetValue()[0] != '{' {
				t.Fatal("wire schema aliases caller-owned bytes")
			}
		})
	}
}

func TestIncompleteReasonAliasesAreLimitedToKnownValues(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{{"max_tokens", "max_tokens"}, {"length", "length"}, {"private-model-output", ""}} {
		response := responseFixture(t, `{"cards":[`, 8192)
		response.Status = responses.ResponseStatus_incomplete
		response.IncompleteDetails = &responses.IncompleteDetails{Reason: tc.reason}
		m, _ := New(Options{ModelID: "fixture", MaxOutputTokens: 8192, Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
			return response, nil
		}})
		result, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageEncode, SystemPrompt: "JSON", InputJSON: json.RawMessage(`{}`)})
		_, code, param := SafeTransportDetails(err)
		if code != "response_incomplete" || param != tc.want || result.Metadata.OutputTokens != 8192 || len(result.JSON) != 0 {
			t.Fatalf("alias/usage guard mismatch code=%s param=%s", code, param)
		}
	}
}

func responseFixture(t *testing.T, text string, outputTokens int64) *responses.ResponseObject {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}}, "usage": map[string]any{"input_tokens": 21, "output_tokens": outputTokens, "total_tokens": 21 + outputTokens}})
	if err != nil {
		t.Fatal(err)
	}
	var out responses.ResponseObject
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestIncompleteResponseIsTransportFailureWithUsageAndNoPartialJSON(t *testing.T) {
	response := responseFixture(t, `{"cards":[`, 8192)
	response.Status = responses.ResponseStatus_incomplete
	response.IncompleteDetails = &responses.IncompleteDetails{Reason: "max_output_tokens"}
	m, _ := New(Options{ModelID: "fixture", MaxOutputTokens: 8192, Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
		return response, nil
	}})
	result, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageEncode, SystemPrompt: "JSON", InputJSON: json.RawMessage(`{}`)})
	status, code, param := SafeTransportDetails(err)
	if !errors.Is(err, ErrTransport) || status != 200 || code != "response_incomplete" || param != "max_output_tokens" || len(result.JSON) != 0 {
		t.Fatalf("incomplete output incorrectly accepted: code=%s param=%s", code, param)
	}
	if result.Metadata.InputTokens != 21 || result.Metadata.OutputTokens != 8192 {
		t.Fatal("incomplete response lost actual usage")
	}
}

func TestOnlyExplicitCompletedResponseStatusIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		status responses.ResponseStatus_Enum
		code   string
	}{{responses.ResponseStatus_unspecified, "response_status_missing"}, {responses.ResponseStatus_in_progress, "response_in_progress"}, {responses.ResponseStatus_failed, "response_failed"}, {responses.ResponseStatus_Enum(99), "response_status_invalid"}} {
		t.Run(tc.code, func(t *testing.T) {
			response := responseFixture(t, `{}`, 5)
			response.Status = tc.status
			response.IncompleteDetails = &responses.IncompleteDetails{Reason: "private-model-output"}
			m, _ := New(Options{ModelID: "fixture", Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
				return response, nil
			}})
			result, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageQuery, SystemPrompt: "JSON", InputJSON: json.RawMessage(`{}`)})
			_, code, _ := SafeTransportDetails(err)
			if !errors.Is(err, ErrTransport) || code != tc.code || len(result.JSON) != 0 || result.Metadata.OutputTokens != 5 {
				t.Fatalf("invalid response status accepted or lost usage: %v", err)
			}
			if strings.Contains(fmt.Sprint(err), "private") {
				t.Fatal("raw incomplete reason leaked")
			}
		})
	}
}

func TestOutputLimitsRejectOversizeWithUsageButAllowExactCompletedBudget(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		tokens     int64
		wantCode   string
	}{{"bytes", strings.Repeat("x", (256<<10)+1), 5, "output_size_limit"}, {"tokens", `{}`, 513, "output_token_limit"}, {"exact", `{}`, 512, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			response := responseFixture(t, tc.text, tc.tokens)
			m, _ := New(Options{ModelID: "fixture", MaxOutputTokens: 512, Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
				return response, nil
			}})
			result, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageQuery, SystemPrompt: "JSON", InputJSON: json.RawMessage(`{}`)})
			_, code, _ := SafeTransportDetails(err)
			if code != tc.wantCode || result.Metadata.OutputTokens != int(tc.tokens) {
				t.Fatalf("limit/usage mismatch: code=%s", code)
			}
			if tc.wantCode != "" && len(result.JSON) != 0 {
				t.Fatal("oversize output exposed as repairable JSON")
			}
			if tc.wantCode == "" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProviderFailureKeepsSafeStatusCodeParamWithoutRawMessage(t *testing.T) {
	param := "reasoning.effort"
	m, _ := New(Options{ModelID: "fixture", Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
		return nil, fmt.Errorf("outer private-url: %w", &arkmodel.APIError{HTTPStatusCode: 400, Code: "InvalidParameter.UnsupportedParameter", Param: &param, Message: "private-key private-config raw-model-text", RequestId: "private-request"})
	}})
	_, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageQuery, SystemPrompt: "JSON only", InputJSON: json.RawMessage(`{}`)})
	var transport *TransportError
	if !errors.Is(err, ErrTransport) || !errors.As(err, &transport) || transport.HTTPStatus != 400 || transport.Code != "InvalidParameter.UnsupportedParameter" || transport.Param != param {
		t.Fatalf("safe provider detail missing: %v", err)
	}
	for _, secret := range []string{"private", "raw-model"} {
		if strings.Contains(fmt.Sprintf("%+v", err), secret) {
			t.Fatal("provider content leaked")
		}
	}
}

func TestProviderDiagnosticsRejectUnsafeFieldsAndPreserveRequestStatus(t *testing.T) {
	unsafeParam := "https://private.invalid/secret"
	for _, tc := range []struct {
		upstream error
		status   int
		code     string
	}{
		{&arkmodel.RequestError{HTTPStatusCode: 503, Err: errors.New("private-url private-secret"), RequestId: "private-request"}, 503, "request_error"},
		{&arkmodel.APIError{HTTPStatusCode: 400, Code: "bad\nprivate-secret", Message: "private-body", Param: &unsafeParam}, 400, "unknown"},
		{errors.New("private-transport-error"), 0, "unknown"},
	} {
		safe := safeTransportError(tc.upstream)
		status, code, param := SafeTransportDetails(safe)
		if !errors.Is(safe, ErrTransport) || status != tc.status || code != tc.code || param != "" {
			t.Fatalf("unexpected safe detail status=%d code=%s param=%s", status, code, param)
		}
		if strings.Contains(fmt.Sprintf("%+v", safe), "private") {
			t.Fatal("sensitive provider text leaked")
		}
	}
}

type countGate struct{ acquired, released int }

func (g *countGate) Acquire(context.Context, string) (func(), error) {
	g.acquired++
	return func() { g.released++ }, nil
}
func TestProviderFailureStillReleasesSharedModelGate(t *testing.T) {
	gate := &countGate{}
	m, _ := New(Options{ModelID: "fixture", Gate: gate, Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
		return nil, &arkmodel.APIError{HTTPStatusCode: 400, Code: "InvalidEndpoint.ClosedEndpoint"}
	}})
	_, err := m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageEncode, SystemPrompt: "JSON only", InputJSON: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrTransport) || gate.acquired != 1 || gate.released != 1 {
		t.Fatal("failed provider call changed gate accounting")
	}
}

func TestModelRejectsInvalidInputBeforeRemoteCall(t *testing.T) {
	calls := 0
	m, err := New(Options{ModelID: "model", Call: func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error) {
		calls++
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Complete(context.Background(), memes.CompletionRequest{Stage: memes.StageQuery, SystemPrompt: "instructions", InputJSON: json.RawMessage(`invalid`)}); err == nil || calls != 0 {
		t.Fatalf("invalid request reached provider: calls=%d err=%v", calls, err)
	}
}
