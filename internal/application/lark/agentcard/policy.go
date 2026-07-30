package agentcard

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type PolicyConfig struct {
	AllowedHTTPSDomains []string
	RiskyIntents        []string
}

func CheckPolicy(spec CardSpec, config PolicyConfig) []ValidationIssue {
	checker := policyChecker{
		allowedDomains: make(map[string]struct{}, len(config.AllowedHTTPSDomains)),
		riskyIntents:   make(map[string]struct{}, len(config.RiskyIntents)),
		activeSections: make(map[*SectionBlock]bool),
		activeColumns:  make(map[*ColumnsBlock]bool),
	}
	for _, domain := range config.AllowedHTTPSDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			checker.allowedDomains[domain] = struct{}{}
		}
	}
	for _, intent := range config.RiskyIntents {
		intent = strings.TrimSpace(intent)
		if intent != "" {
			checker.riskyIntents[intent] = struct{}{}
		}
	}
	for index := range spec.Blocks {
		checker.visitBlock(spec.Blocks[index], fmt.Sprintf("$.blocks[%d]", index))
	}
	for index, action := range spec.Actions {
		path := fmt.Sprintf("$.actions[%d]", index)
		if _, risky := checker.riskyIntents[action.Intent]; risky &&
			action.Mode != ActionModeCapabilityConfirm {
			checker.add("risky_intent_requires_confirmation", path+".mode")
		}
		if action.Mode == ActionModeServer &&
			looksLikeAgentContinuation(action.Intent) {
			checker.add("server_action_continuation_denied", path+".intent")
		}
		if action.URL != "" {
			checker.checkURL(action.URL, path+".url")
		}
	}
	sort.SliceStable(checker.issues, func(i, j int) bool {
		if checker.issues[i].Path == checker.issues[j].Path {
			return checker.issues[i].Code < checker.issues[j].Code
		}
		return checker.issues[i].Path < checker.issues[j].Path
	})
	return checker.issues
}

type policyChecker struct {
	issues         []ValidationIssue
	allowedDomains map[string]struct{}
	riskyIntents   map[string]struct{}
	activeSections map[*SectionBlock]bool
	activeColumns  map[*ColumnsBlock]bool
}

func (c *policyChecker) visitBlock(block Block, path string) {
	switch block.Kind {
	case BlockColumns:
		if block.Columns == nil || c.activeColumns[block.Columns] {
			return
		}
		c.activeColumns[block.Columns] = true
		for columnIndex, column := range block.Columns.Columns {
			for blockIndex := range column.Blocks {
				c.visitBlock(
					column.Blocks[blockIndex],
					fmt.Sprintf(
						"%s.columns[%d].blocks[%d]",
						path,
						columnIndex,
						blockIndex,
					),
				)
			}
		}
		delete(c.activeColumns, block.Columns)
	case BlockSection:
		if block.Section == nil || c.activeSections[block.Section] {
			return
		}
		c.activeSections[block.Section] = true
		for index := range block.Section.Blocks {
			c.visitBlock(
				block.Section.Blocks[index],
				fmt.Sprintf("%s.blocks[%d]", path, index),
			)
		}
		delete(c.activeSections, block.Section)
	case BlockTextInput:
		if block.TextInput != nil {
			c.checkInput(
				block.TextInput.Field,
				block.TextInput.Config.Placeholder,
				path,
			)
		}
	case BlockSingleSelect:
		if block.SingleSelect != nil {
			c.checkInput(
				block.SingleSelect.Field,
				block.SingleSelect.Placeholder,
				path,
			)
		}
	case BlockMultiSelect:
		if block.MultiSelect != nil {
			c.checkInput(
				block.MultiSelect.Field,
				block.MultiSelect.Placeholder,
				path,
			)
		}
	}
}

func (c *policyChecker) checkInput(field InputField, placeholder, path string) {
	semanticText := strings.ToLower(strings.Join([]string{
		field.FieldID,
		field.Label,
		field.Purpose,
		placeholder,
	}, " "))
	if containsSensitiveSemantic(semanticText) {
		c.add("sensitive_field", path+".field_id")
	}
}

func (c *policyChecker) checkURL(raw, path string) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "data:") {
		c.add("data_url_denied", path)
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		c.add("invalid_url", path)
		return
	}
	if strings.ToLower(parsed.Scheme) != "https" {
		c.add("https_required", path)
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := false
	for domain := range c.allowedDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			allowed = true
			break
		}
	}
	if !allowed {
		c.add("url_domain_denied", path)
	}
	for key := range parsed.Query() {
		if containsTokenSemantic(strings.ToLower(key)) {
			c.add("token_query_denied", path)
			break
		}
	}
}

func (c *policyChecker) add(code, path string) {
	c.issues = append(c.issues, ValidationIssue{Code: code, Path: path})
}

func containsSensitiveSemantic(value string) bool {
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential", "api key",
		"api_key", "otp", "one-time password", "verification code",
		"验证码", "密码", "密钥", "令牌", "凭证", "身份证", "银行卡",
		"bank card", "id card",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func containsTokenSemantic(value string) bool {
	for _, marker := range []string{
		"token", "secret", "credential", "api_key", "apikey", "signature", "sign",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func looksLikeAgentContinuation(intent string) bool {
	value := strings.ToLower(strings.TrimSpace(intent))
	return strings.Contains(value, "continue_agent") ||
		strings.Contains(value, "resume_agent") ||
		strings.Contains(value, "agent_continue")
}
