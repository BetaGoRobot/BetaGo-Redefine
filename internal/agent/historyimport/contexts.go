// Package historyimport implements the offline, human-only history import path.
// It never participates in conversations or calls model/provider write APIs.
package historyimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

const MaxContextBytes = 16 << 10
const MaxContextSources = 64
const ContextGap = 24 * time.Hour

func humanMetadata(m domain.Message) (domain.IngressMetadata, error) {
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil || meta.SenderType != "user" || (meta.Kind != "refresh" && meta.Kind != "backfill" && meta.Kind != "message" && meta.Kind != "archive") || meta.Control != nil || m.Deleted {
		return meta, fmt.Errorf("%w: verified human source required", domain.ErrInvalid)
	}
	if err := m.Source.Validate(); err != nil {
		return meta, err
	}
	if m.UpdatedAt.IsZero() || m.UpdatedAt.Before(m.Source.OccurredAt) || !utf8.ValidString(m.Text) {
		return meta, fmt.Errorf("%w: canonical source content or time", domain.ErrInvalid)
	}
	return meta, nil
}

func memoryText(m domain.Message) bool {
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil || strings.TrimSpace(m.Text) == "" {
		return false
	}
	return meta.MessageType == "text" || meta.MessageType == "post" || meta.MessageType == ""
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func frame(author string) string {
	return "[本群历史发言证据；每段作者=" + author + "；第一人称指该作者；引述、转发、提及不代表本人经历；勿把玩笑或疑问当确定事实。]\n"
}
func header(m domain.Message, part string) string {
	return fmt.Sprintf("[message=%s author=%s at=%s version=%d part=%s%s]\n", m.Source.MessageID, m.Source.AuthorID, m.Source.OccurredAt.UTC().Format(time.RFC3339Nano), m.Source.Version, part, archiveDisplayName(m))
}

// BuildContexts validates all facts before returning any output. Blocks contain
// one author, bounded unique references and UTF-8 bytes including provenance.
// Oversized messages use byte-offset part IDs and preserve every source byte.
func BuildContexts(scope domain.Scope, input []domain.Message) ([]domain.Context, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if scope.Visibility != "group" {
		return nil, fmt.Errorf("%w: group scope required", domain.ErrInvalid)
	}
	messages := make([]domain.Message, 0, len(input))
	seen := map[string]domain.Message{}
	for _, m := range input {
		if _, err := humanMetadata(m); err != nil {
			return nil, err
		}
		if m.Source.Scope != scope {
			return nil, fmt.Errorf("%w: cross-scope source", domain.ErrInvalid)
		}
		if previous, ok := seen[m.Source.MessageID]; ok {
			if digest(previous) != digest(m) {
				return nil, fmt.Errorf("%w: duplicate source", domain.ErrConflict)
			}
			continue
		}
		seen[m.Source.MessageID] = m
		if memoryText(m) {
			messages = append(messages, m.Clone())
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		a, b := messages[i].Source, messages[j].Source
		if a.AuthorID != b.AuthorID {
			return a.AuthorID < b.AuthorID
		}
		if !a.OccurredAt.Equal(b.OccurredAt) {
			return a.OccurredAt.Before(b.OccurredAt)
		}
		return a.MessageID < b.MessageID
	})
	var out []domain.Context
	var block domain.Context
	var last time.Time
	flush := func() {
		if len(block.Sources) == 0 {
			return
		}
		block.ID = "history:" + digest(struct {
			Scope   domain.Scope
			Text    string
			Sources []domain.SourceRef
		}{scope, block.Text, block.Sources})
		out = append(out, block)
		block = domain.Context{}
	}
	appendPart := func(m domain.Message, text string) {
		if len(block.Sources) == 0 {
			block = domain.Context{Scope: scope, Version: 1, CreatedAt: m.Source.OccurredAt, UpdatedAt: m.UpdatedAt, Text: frame(m.Source.AuthorID)}
		}
		block.Sources = append(block.Sources, m.Source)
		block.Text += text
		if m.UpdatedAt.After(block.UpdatedAt) {
			block.UpdatedAt = m.UpdatedAt
		}
		last = m.Source.OccurredAt
	}
	for _, m := range messages {
		whole := header(m, "whole") + m.Text + "\n"
		if len(frame(m.Source.AuthorID))+len(whole) > MaxContextBytes {
			flush()
			for start := 0; start < len(m.Text); {
				partID := fmt.Sprintf("%d", start)
				prefix := header(m, partID)
				budget := MaxContextBytes - len(frame(m.Source.AuthorID)) - len(prefix) - 1
				if budget < utf8.UTFMax {
					return nil, fmt.Errorf("%w: provenance exceeds context byte budget", domain.ErrInvalid)
				}
				end := min(start+budget, len(m.Text))
				for end < len(m.Text) && !utf8.RuneStart(m.Text[end]) {
					end--
				}
				appendPart(m, prefix+m.Text[start:end]+"\n")
				flush()
				start = end
			}
			continue
		}
		if len(block.Sources) > 0 && (block.Sources[0].AuthorID != m.Source.AuthorID || m.Source.OccurredAt.Sub(last) > ContextGap || len(block.Sources) >= MaxContextSources || len(block.Text)+len(whole) > MaxContextBytes) {
			flush()
		}
		appendPart(m, whole)
	}
	flush()
	for _, c := range out {
		if len(c.Text) > MaxContextBytes || strings.TrimSpace(c.Text) == "" {
			return nil, fmt.Errorf("%w: context byte budget", domain.ErrInvalid)
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// A historical display name is evidence from that archived message, never an
// identity join. JSON quoting prevents a stored name from injecting headers.
func archiveDisplayName(m domain.Message) string {
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil {
		return ""
	}
	var p archiveProvenance
	if json.Unmarshal(meta.Provider, &p) != nil || p.Source != archiveSource {
		return ""
	}
	var row archiveRowRecord
	if json.Unmarshal(p.Row, &row) != nil {
		return ""
	}
	var source struct {
		Name string `json:"user_name"`
	}
	if json.Unmarshal(row.Source, &source) != nil || source.Name == "" {
		return ""
	}
	name := source.Name
	if len(name) > 256 {
		end := 256
		for !utf8.RuneStart(name[end]) {
			end--
		}
		name = name[:end] + "…"
	}
	quoted, _ := json.Marshal(name)
	return " archived_display_name=" + string(quoted)
}
