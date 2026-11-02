package config

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var name = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]{0,62}$`)
var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func safe(s string) bool { return len(s) > 0 && len(s) <= 8192 && !strings.ContainsAny(s, "\r\n\x00") }
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && len(s) <= 2048 && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func endpoint(e Endpoint, protocols ...string) bool {
	if !httpURL(e.BaseURL) || !safe(e.APIKey) || !safe(e.Model) || e.TimeoutSeconds < 1 || e.TimeoutSeconds > 300 {
		return false
	}
	for _, p := range protocols {
		if e.Protocol == p {
			return true
		}
	}
	return false
}

func (c Config) Validate(mode string) error {
	if mode != "agent" && mode != "worker" && mode != "models" {
		return errors.New("unknown agent mode")
	}
	if !endpoint(c.Models.Embedding.Endpoint, "ark_multimodal", "openai") || c.Models.Embedding.Dimensions < 1 || c.Models.Embedding.Dimensions > 2000 || c.Models.Embedding.Concurrency < 1 || c.Models.Embedding.Concurrency > 32 || c.Models.Embedding.MaxInputs < 1 || c.Models.Embedding.MaxInputs > 128 {
		return errors.New("invalid embedding role configuration")
	}
	if !endpoint(c.Models.Rerank, "json_chat", "cohere") {
		return errors.New("invalid reranking role configuration")
	}
	if mode == "models" {
		if !safe(c.Gateway.APIKey) || !safe(c.Gateway.EmbeddingModel) || !safe(c.Gateway.RerankModel) || c.Gateway.MaxBodyBytes < 1024 || c.Gateway.MaxBodyBytes > 32<<20 || c.Gateway.TimeoutSeconds < 1 || c.Gateway.TimeoutSeconds > 300 {
			return errors.New("invalid model gateway configuration")
		}
		return nil
	}
	db, err := url.Parse(c.Database.URL)
	if err != nil || db.Hostname() == "" || (db.Scheme != "postgres" && db.Scheme != "postgresql") || !schemaName.MatchString(c.Database.Schema) || c.Database.MaxConnections < 1 || c.Database.MaxConnections > 64 {
		return errors.New("invalid independent database configuration")
	}
	if (c.App.TenantID != "" && !safe(c.App.TenantID)) || c.App.Visibility != "group" || len(c.App.AllowedChatIDs) > 1000 {
		return errors.New("invalid group scope configuration")
	}
	seen := map[string]bool{}
	for _, id := range c.App.AllowedChatIDs {
		if !safe(id) || seen[id] {
			return errors.New("invalid group allowlist")
		}
		seen[id] = true
	}
	if !safe(c.Lark.AppID) || !safe(c.Lark.AppSecret) || !safe(c.Lark.BotOpenID) || (c.Lark.Transport != "websocket" && c.Lark.Transport != "callback") {
		return errors.New("invalid Lark identity or transport configuration")
	}
	if c.Lark.Transport == "callback" && (!safe(c.Lark.VerificationToken) || !safe(c.Lark.EncryptionKey)) {
		return errors.New("callback verification credentials are required")
	}
	if !httpURL(c.Search.Endpoint) || !safe(c.Search.APIKey) || !name.MatchString(c.Search.MessagesIndex) || !name.MatchString(c.Search.ContextsIndex) || c.Search.MessagesIndex == c.Search.ContextsIndex {
		return errors.New("invalid search configuration")
	}
	if !httpURL(c.Memory.Endpoint) || !safe(c.Memory.APIKey) {
		return errors.New("invalid long-term memory configuration")
	}
	host, port, err := net.SplitHostPort(c.Objects.Endpoint)
	if err != nil || host == "" || port == "" || !safe(c.Objects.AccessKey) || !safe(c.Objects.SecretKey) || !name.MatchString(c.Objects.Bucket) {
		return errors.New("invalid object storage configuration")
	}
	if !endpoint(c.Models.Answer, "openai") {
		return errors.New("invalid answer model configuration")
	}
	for _, e := range []Endpoint{c.Models.Vision, c.Models.Audio} {
		if e.Model != "" && !endpoint(e, "openai") {
			return errors.New("invalid media model configuration")
		}
	}
	if c.Worker.Concurrency < 1 || c.Worker.Concurrency > 32 || c.Worker.LeaseSeconds < 10 || c.Worker.LeaseSeconds > 600 || c.Worker.PollMilliseconds < 10 || c.Worker.PollMilliseconds > 10000 {
		return errors.New("invalid worker limits")
	}
	if c.Runtime.MaxConcurrentTasks < 1 || c.Runtime.MaxConcurrentTasks > 32 || c.Runtime.MaxTurns < 1 || c.Runtime.MaxTurns > 64 || c.Runtime.MaxContextBytes < 8192 || c.Runtime.MaxContextBytes > 1<<20 || c.Runtime.TurnLeaseSeconds < 10 || c.Runtime.TurnLeaseSeconds > 600 || c.Runtime.TaskTimeoutSeconds < 60 || c.Runtime.TaskTimeoutSeconds > 86400 {
		return errors.New("invalid task runtime limits")
	}
	if mode == "agent" && (!httpURL(c.App.PublicURL) || !safe(c.App.APIKey)) {
		return errors.New("invalid Agent API configuration")
	}
	if mode == "agent" {
		if _, err := c.MemeSettings(); err != nil {
			return err
		}
		if err := c.Capabilities.validate(); err != nil {
			return err
		}
		if c.Models.WebSearch.Model != "" && !endpoint(c.Models.WebSearch, "ark_responses") {
			return errors.New("invalid public web search model configuration")
		}
		return c.validateTools()
	}
	return nil
}
