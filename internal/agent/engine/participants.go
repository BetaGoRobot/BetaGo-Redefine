package engine

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// GroupMembersReader supplies display facts only. Membership snapshots are
// deliberately independent of task ownership, policy and approval decisions.
type GroupMembersReader interface {
	GroupMembers(context.Context, domain.Scope, int, string) (domain.GroupMembers, error)
}

const (
	memberCacheTTL      = time.Minute
	memberCacheCapacity = 64
	memberPageLimit     = 4
	memberLimit         = 400
)

type memberDirectory struct{ Complete, Unavailable, Truncated bool }
type memberSnapshot struct {
	people map[string]domain.GroupMember
	status memberDirectory
}
type memberCacheEntry struct {
	snapshot memberSnapshot
	expires  time.Time
}
type memberCache struct {
	mu      sync.Mutex
	entries map[domain.Scope]memberCacheEntry
}

func (e *Engine) memberSnapshot(ctx context.Context, scope domain.Scope) (memberSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return memberSnapshot{}, err
	}
	if scope.Validate() != nil {
		return memberSnapshot{}, domain.ErrInvalid
	}
	now := time.Now()
	e.members.mu.Lock()
	entry, ok := e.members.entries[scope]
	e.members.mu.Unlock()
	// Display names are local facts, not authorization. Do not periodically
	// refetch a successful directory just to render another chat turn.
	if ok && (!entry.snapshot.status.Unavailable || now.Before(entry.expires)) {
		return entry.snapshot, nil
	}
	snapshot := memberSnapshot{people: map[string]domain.GroupMember{}}
	if e.c.GroupMembers == nil {
		snapshot.status.Unavailable = true
		return snapshot, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	token := ""
	seenTokens := map[string]bool{}
	for page := 0; page < memberPageLimit; page++ {
		batch, err := e.c.GroupMembers.GroupMembers(readCtx, scope, 100, token)
		if err != nil {
			if ctx.Err() != nil {
				return memberSnapshot{}, ctx.Err()
			}
			snapshot.status.Unavailable = true
			break
		}
		for _, person := range batch.Members {
			if !displayID(person.ID) || person.IDType != "open_id" {
				snapshot.status.Truncated = true
				continue
			}
			if _, exists := snapshot.people[person.ID]; !exists && len(snapshot.people) == memberLimit {
				snapshot.status.Truncated = true
				break
			}
			person.Name = displayName(person.Name)
			snapshot.people[person.ID] = person
		}
		if batch.SecurityLimited {
			snapshot.status.Truncated = true
		}
		if !batch.HasMore {
			snapshot.status.Complete = !snapshot.status.Truncated
			break
		}
		if len(snapshot.people) == memberLimit || page == memberPageLimit-1 || batch.PageToken == "" || seenTokens[batch.PageToken] {
			snapshot.status.Truncated = true
			break
		}
		token = batch.PageToken
		seenTokens[token] = true
	}
	if err := ctx.Err(); err != nil {
		return memberSnapshot{}, err
	}
	e.members.mu.Lock()
	defer e.members.mu.Unlock()
	if e.members.entries == nil {
		e.members.entries = map[domain.Scope]memberCacheEntry{}
	}
	if len(e.members.entries) >= memberCacheCapacity {
		var oldestScope domain.Scope
		var oldest time.Time
		for key, v := range e.members.entries {
			if oldest.IsZero() || v.expires.Before(oldest) {
				oldestScope, oldest = key, v.expires
			}
		}
		delete(e.members.entries, oldestScope)
	}
	ttl := memberCacheTTL
	if snapshot.status.Unavailable {
		ttl = 5 * time.Second
	}
	e.members.entries[scope] = memberCacheEntry{snapshot: snapshot, expires: time.Now().Add(ttl)}
	return snapshot, nil
}

func displayID(s string) bool {
	return s != "" && len(s) <= 1024 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}
func displayName(s string) string {
	if !utf8.ValidString(s) {
		return ""
	}
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	return truncateUTF8(s, 256)
}

type participant struct{ MemberRef, ID, Name, Role, NameOrigin string }
type participantSet struct {
	scope    domain.Scope
	botID    string
	snapshot memberSnapshot
	people   map[string]participant
}

func newParticipantSet(scope domain.Scope, botID string, snapshot memberSnapshot) *participantSet {
	return &participantSet{scope: scope, botID: botID, snapshot: snapshot, people: map[string]participant{}}
}
func (p *participantSet) person(id, role string) participant {
	if role == "assistant" || (p.botID != "" && id == p.botID) {
		person := participant{MemberRef: "self", ID: p.botID, Name: "AI", Role: "assistant", NameOrigin: "self"}
		p.people[person.MemberRef] = person
		return person
	}
	ref := "member_" + domain.StableID(p.scope.Key(), id)[:12]
	if person, exists := p.people[ref]; exists {
		// Mentions supply no sender-role fact. A verified message can resolve
		// an unknown role, and a bot sender must survive later mention hints.
		if role != "unknown" && (person.Role == "unknown" || role == "bot") {
			person.Role = role
			p.people[ref] = person
		}
		return person
	}
	person := participant{MemberRef: ref, ID: id, Name: "unknown_" + ref[len("member_"):], Role: role, NameOrigin: "unknown"}
	if member, exists := p.snapshot.people[id]; exists && member.Name != "" {
		person.Name, person.NameOrigin = member.Name, "group_members"
	}
	p.people[ref] = person
	return person
}
func (p *participantSet) selected(messages []sceneMessage) []participant {
	refs := map[string]bool{}
	for _, m := range messages {
		refs[m.SpeakerRef] = true
		for _, mention := range m.Mentions {
			if mention.MemberRef != "" {
				refs[mention.MemberRef] = true
			}
		}
		if m.ReplyTo != nil && m.ReplyTo.SpeakerRef != "" {
			refs[m.ReplyTo.SpeakerRef] = true
		}
	}
	out := make([]participant, 0, len(refs))
	for ref := range refs {
		if person, exists := p.people[ref]; exists {
			out = append(out, person)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MemberRef < out[j].MemberRef })
	return out
}
