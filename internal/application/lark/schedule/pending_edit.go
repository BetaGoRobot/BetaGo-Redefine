package schedule

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

// PendingEdit represents an in-memory legacy edit confirmation. It is used only
// when the request has no Conversation Runtime envelope; durable runtime waits
// persist their trusted capability inputs outside this fallback map.
type PendingEdit struct {
	TaskID      string
	ActorOpenID string
	NewValues   map[string]any
	CreatedAt   time.Time
}

var (
	pendingEdits              = make(map[string]*PendingEdit)
	pendingEditTimers         = make(map[string]*time.Timer)
	pendingEditGenerations    = make(map[string]uint64)
	pendingEditGenerationNext uint64
	pendingEditsMu            sync.RWMutex
	pendingEditTTL            = 10 * time.Minute
)

func generateEditToken() string {
	now := time.Now().UnixNano()
	randStr, _ := randomString(8)
	return strings.TrimSpace("edit_" + formatNanoTime(now) + "_" + randStr)
}

func randomString(n int) (string, error) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b), nil
}

func formatNanoTime(t int64) string {
	return time.Unix(0, t).Format("20060102150405")
}

func storePendingEdit(token string, edit *PendingEdit) error {
	return storeLegacyPendingEdit(token, edit)
}

// storeLegacyPendingEdit preserves the old token callback path for cards that
// were created without a Conversation Runtime envelope.
func storeLegacyPendingEdit(token string, edit *PendingEdit) error {
	pendingEditsMu.Lock()
	defer pendingEditsMu.Unlock()
	if timer := pendingEditTimers[token]; timer != nil {
		timer.Stop()
	}
	pendingEditGenerationNext++
	generation := pendingEditGenerationNext
	pendingEdits[token] = edit
	pendingEditGenerations[token] = generation

	// Publish the new generation and timer while holding the same lock. Even an
	// immediate callback cannot observe partially published state.
	pendingEditTimers[token] = time.AfterFunc(pendingEditTTL, func() {
		expireLegacyPendingEdit(token, generation)
	})
	return nil
}

func expireLegacyPendingEdit(token string, generation uint64) {
	pendingEditsMu.Lock()
	defer pendingEditsMu.Unlock()
	if pendingEditGenerations[token] != generation {
		return
	}
	if timer := pendingEditTimers[token]; timer != nil {
		timer.Stop()
	}
	delete(pendingEdits, token)
	delete(pendingEditTimers, token)
	delete(pendingEditGenerations, token)
}

func GetPendingEdit(token string) (*PendingEdit, bool) {
	pendingEditsMu.RLock()
	defer pendingEditsMu.RUnlock()
	edit, ok := pendingEdits[token]
	return edit, ok
}

func DeletePendingEdit(token string) {
	pendingEditsMu.Lock()
	defer pendingEditsMu.Unlock()
	if timer := pendingEditTimers[token]; timer != nil {
		timer.Stop()
	}
	delete(pendingEditTimers, token)
	delete(pendingEdits, token)
	delete(pendingEditGenerations, token)
}
