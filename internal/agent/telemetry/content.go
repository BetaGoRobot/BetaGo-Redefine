package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Business content is deliberately readable. Only credentials are removed,
// including credentials nested inside JSON tool results or model messages.
var bearerCredential = regexp.MustCompile(`(?i)\bBearer\s+[a-zA-Z0-9._~+/=-]+`)
var headerCredential = regexp.MustCompile("(?im)(\\b(?:proxy-authorization|authorization|cookie|set-cookie)\\s*[:=]\\s*)[^\\r\\n]+")
var namedCredential = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|app[_-]?secret|client[_-]?secret|password)["']?\s*[:=]\s*)(?:\[redacted\]|"[^"]*"|'[^']*'|[^\s"',;&}\]]+)`)

func credentialKey(key string) bool {
	if at := strings.LastIndexByte(key, '.'); at >= 0 && credentialKey(key[at+1:]) {
		return true
	}
	key = strings.ToLower(strings.NewReplacer("_", "", ".", "", "-", "", " ", "").Replace(key))
	switch key {
	case "authorization", "proxyauthorization", "password", "passwd", "secret", "credential", "credentials", "apikey", "token", "accesstoken", "refreshtoken", "idtoken", "appsecret", "clientsecret", "cookie", "setcookie", "bindingurl", "signedurl":
		return true
	}
	return strings.HasSuffix(key, "token") || strings.HasSuffix(key, "secret") || strings.HasSuffix(key, "credential") || strings.HasSuffix(key, "password") || strings.HasSuffix(key, "apikey") || strings.HasSuffix(key, "accesstoken") || strings.HasSuffix(key, "refreshtoken") || strings.HasSuffix(key, "clientsecret") || strings.HasSuffix(key, "appsecret")
}

func redactText(value string) string {
	return namedCredential.ReplaceAllString(headerCredential.ReplaceAllString(bearerCredential.ReplaceAllString(value, "Bearer [redacted]"), "${1}[redacted]"), "${1}[redacted]")
}

func sanitizeValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if credentialKey(key) {
				v[key] = "[redacted]"
			} else {
				v[key] = sanitizeValue(item)
			}
		}
	case []any:
		for i, item := range v {
			v[i] = sanitizeValue(item)
		}
	case string:
		return sanitizeText(v)
	}
	return value
}

func sanitizeText(value string) string {
	// Most plaintext is not JSON. Avoid a JSON allocation for ordinary prose.
	t := strings.TrimSpace(value)
	if len(t) > 1 && (t[0] == '{' || t[0] == '[') {
		var v any
		dec := json.NewDecoder(strings.NewReader(t))
		dec.UseNumber()
		if json.Valid([]byte(t)) && dec.Decode(&v) == nil {
			b, err := json.Marshal(sanitizeValue(v))
			if err == nil {
				return string(b)
			}
		}
	}
	return redactText(value)
}

// sanitizedContent is private to Payload. It prevents reinterpreting individual
// chunks as new JSON or credential assignments after the complete payload was
// sanitized and hashed.
type sanitizedContent string

func businessAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindAny {
		if _, ok := a.Value.Any().(sanitizedContent); ok {
			return a
		}
	}
	a.Value = a.Value.Resolve()
	if credentialKey(a.Key) {
		return slog.String(a.Key, "[redacted]")
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, sanitizeText(a.Value.String()))
	case slog.KindGroup:
		attrs := a.Value.Group()
		clean := make([]slog.Attr, 0, len(attrs))
		for _, item := range attrs {
			clean = append(clean, businessAttr(item))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(clean...)}
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			_, code := Classify(err)
			return slog.String(a.Key, code)
		}
		b, err := json.Marshal(a.Value.Any())
		if err != nil {
			return slog.String(a.Key, "[unencodable]")
		}
		return slog.String(a.Key, sanitizeText(string(b)))
	}
	return a
}

// Payload writes the complete sanitized content in UTF-8-safe chunks. Large
// model contexts must not exceed the collector/storage per-record size limit.
func Payload(ctx context.Context, event, field string, value any) {
	if suppressed, _ := ctx.Value(withoutPayloadKey{}).(bool); suppressed {
		return
	}
	var content string
	switch v := value.(type) {
	case string:
		content = v
	case []byte:
		content = string(v)
	case json.RawMessage:
		content = string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			Warn(ctx, event+".encode_failed", "field", field)
			return
		}
		content = string(b)
	}
	content = sanitizeText(content)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	const limit = 24 << 10
	chunks := []string{}
	for len(content) > limit {
		end := limit
		for !utf8.RuneStart(content[end]) {
			end--
		}
		chunks = append(chunks, content[:end])
		content = content[end:]
	}
	chunks = append(chunks, content)
	var size int
	for _, chunk := range chunks {
		size += len(chunk)
	}
	for i, chunk := range chunks {
		Info(ctx, event, slog.Any(field, sanitizedContent(chunk)), "payload_field", field, "payload_bytes", size, "payload_sha256", digest, "chunk_index", i+1, "chunk_count", len(chunks))
	}
}

func LogsURL(traceID, spanID string) string {
	q := fmt.Sprintf(`trace_id:="%s"`, traceID)
	if spanID != "" {
		q += fmt.Sprintf(` span_id:="%s"`, spanID)
	}
	values := url.Values{"query": {q}, "g0.range_input": {"1d"}}
	return "https://vlui.kmhomelab.cn/select/vmui/#/?" + values.Encode()
}

func contentPreview(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "…（完整内容见关联日志）"
}
