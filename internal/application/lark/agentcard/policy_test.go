package agentcard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyRejectsSensitiveFieldsAndUnsafeLinksWithoutEchoingValues(t *testing.T) {
	spec := validValidationSpec()
	spec.Blocks = append(spec.Blocks, TextInput("memo", InputField{
		FieldID: "memo", FormID: "schedule_form",
		Label: "备注", Purpose: "填写 API token 和短信验证码",
	}, TextInputConfig{
		Placeholder: "sk-live-super-secret-value", MaxLength: 100,
	}))
	spec.Actions = append(spec.Actions, Action{
		Kind: ActionButton, ID: "open", Label: "打开",
		Mode: ActionModeUI, Intent: "open_help",
		URL: "http://evil.example/redirect?token=sk-live-super-secret-value",
	})
	issues := CheckPolicy(spec, PolicyConfig{
		AllowedHTTPSDomains: []string{"trusted.example"},
	})
	for _, code := range []string{
		"sensitive_field", "https_required", "url_domain_denied", "token_query_denied",
	} {
		if !hasPolicyIssue(issues, code) {
			t.Fatalf("policy issues = %#v, missing %q", issues, code)
		}
	}
	encoded, err := json.Marshal(issues)
	if err != nil {
		t.Fatalf("json.Marshal(issues) error = %v", err)
	}
	if strings.Contains(string(encoded), "sk-live-super-secret-value") {
		t.Fatalf("policy issue leaked field value: %s", encoded)
	}
}

func TestPolicyRejectsRiskyUIAndServerContinuation(t *testing.T) {
	spec := validValidationSpec()
	spec.Actions = []Action{
		{
			Kind: ActionButton, ID: "delete", Label: "删除",
			Mode: ActionModeUI, Intent: "delete_schedule",
		},
		{
			Kind: ActionButton, ID: "continue", Label: "继续",
			Mode: ActionModeServer, Intent: "continue_agent",
		},
	}
	issues := CheckPolicy(spec, PolicyConfig{
		RiskyIntents: []string{"delete_schedule"},
	})
	if !hasPolicyIssue(issues, "risky_intent_requires_confirmation") ||
		!hasPolicyIssue(issues, "server_action_continuation_denied") {
		t.Fatalf("policy issues = %#v", issues)
	}
}

func TestPolicyAllowsTrustedHTTPSLink(t *testing.T) {
	spec := validValidationSpec()
	spec.Actions = append(spec.Actions, Action{
		Kind: ActionButton, ID: "docs", Label: "查看文档",
		Mode: ActionModeUI, Intent: "open_docs",
		URL: "https://docs.trusted.example/schedule",
	})
	issues := CheckPolicy(spec, PolicyConfig{
		AllowedHTTPSDomains: []string{"trusted.example"},
	})
	if len(issues) != 0 {
		t.Fatalf("CheckPolicy() issues = %#v", issues)
	}
}

func TestPolicyRejectsDataURL(t *testing.T) {
	spec := validValidationSpec()
	spec.Actions = append(spec.Actions, Action{
		Kind: ActionButton, ID: "data", Label: "打开",
		Mode: ActionModeUI, Intent: "open",
		URL: "data:text/html;base64,PHNjcmlwdD4=",
	})
	issues := CheckPolicy(spec, PolicyConfig{})
	if !hasPolicyIssue(issues, "data_url_denied") {
		t.Fatalf("policy issues = %#v", issues)
	}
}

func hasPolicyIssue(issues []ValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
