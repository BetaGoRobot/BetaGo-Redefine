package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrUnauthorized = errors.New("agent task actor is not authorized")
var ErrEffectUnknown = errors.New("agent side effect outcome is unknown; automatic replay blocked")
var ErrDeliveryPending = errors.New("agent prior delivery receipt pending")
var ErrTaskBusy = errors.New("agent task already has an active turn")

type Task struct {
	ID                                string
	Scope                             Scope
	OwnerID, RequesterID, LastActorID string
	Delegates                         []string
	Revision                          int64
	Status, Request                   string
	Root                              SourceRef
	Sources, ContextSources           []SourceRef
	InvocationOrigin                  string            `json:",omitempty"`
	ReplyTargets                      []string          `json:",omitempty"`
	FinalConversation                 *ConversationPlan `json:",omitempty"`
	ContextReplans                    int               `json:",omitempty"`
	Facts                             []TaskFact
	ActiveJobID                       int64
	ExpiresAt, CreatedAt, UpdatedAt   time.Time
}
type TaskFact struct {
	Source SourceRef
	Text   string
}

func (t Task) Authorized(actor string) bool {
	if actor == t.OwnerID {
		return true
	}
	for _, id := range t.Delegates {
		if id == actor {
			return true
		}
	}
	return false
}

type TurnRequest struct {
	TaskID     string `json:"task_id"`
	Revision   int64  `json:"revision"`
	ApprovalID string `json:"approval_id,omitempty"`
}
type Approval struct {
	ID, TaskID, Tool, Arguments, InterruptID, ActorID, Status, PolicyDigest string
	Scope                                                                   Scope
	Source                                                                  SourceRef
	Revision                                                                int64
	CreatedAt, DecidedAt                                                    time.Time
}
type ToolExecution struct {
	Key, TaskID, Tool, Arguments, ApprovalID, ActorID, Status, Result, Error string
	Scope                                                                    Scope
	Revision, JobID, Fence                                                   int64
	CreatedAt, UpdatedAt                                                     time.Time
}
type Delivery struct {
	ID, TaskID, ApprovalID, Kind, MessageType, Content, UUID, ParentID, MessageID, Status string
	Scope                                                                                 Scope
	Revision                                                                              int64
	Sources                                                                               []SourceRef `json:",omitempty"`
	BatchID, PreviousID, UpdateOf, ConversationText                                       string      `json:",omitempty"`
	Sequence, BatchSize                                                                   int         `json:",omitempty"`
	CreatedAt, UpdatedAt                                                                  time.Time
	// ConfirmedAt records the first known provider receipt for this frozen
	// delivery. Later lifecycle reconciliation must never rewrite it.
	ConfirmedAt time.Time
}

func StableID(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func StableUUID(parts ...string) string {
	h := StableID(parts...)
	return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-a" + h[17:20] + "-" + h[20:32]
}

// CanonicalArguments rejects duplicate keys, trailing documents, nonobjects and
// identity/scope overrides before freezing the exact approved argument bytes.
func CanonicalArguments(raw string) (string, error) {
	if len(raw) > 65536 || !utf8.ValidString(raw) {
		return "", ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	v, err := jsonValue(d, 0)
	if err != nil {
		return "", fmt.Errorf("%w: tool arguments", ErrInvalid)
	}
	if _, ok := v.(map[string]any); !ok {
		return "", ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return "", ErrInvalid
	}
	b, err := json.Marshal(v)
	return string(b), err
}
func jsonValue(d *json.Decoder, depth int) (any, error) {
	if depth > 40 {
		return nil, ErrInvalid
	}
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := tok.(type) {
	case json.Delim:
		if x == '{' {
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, ErrInvalid
				}
				if _, ok = m[key]; ok {
					return nil, ErrInvalid
				}
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				switch normalized {
				case "scope", "tenantid", "appid", "chatid", "actorid", "callerid", "requesterid", "agentcontext":
					return nil, ErrUnauthorized
				}
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, e := d.Token()
			return m, e
		}
		if x == '[' {
			a := []any{}
			for d.More() {
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e := d.Token()
			return a, e
		}
		return nil, ErrInvalid
	case string, bool, json.Number, nil:
		return tok, nil
	default:
		return nil, ErrInvalid
	}
}
func SameArguments(a, b string) bool {
	ca, e := CanonicalArguments(a)
	if e != nil {
		return false
	}
	cb, e := CanonicalArguments(b)
	return e == nil && bytes.Equal([]byte(ca), []byte(cb))
}
