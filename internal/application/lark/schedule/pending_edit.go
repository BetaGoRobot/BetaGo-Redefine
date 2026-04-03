package schedule

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

// PendingEdit represents a pending edit operation waiting for user confirmation
type PendingEdit struct {
	TaskID      string
	ActorOpenID string
	NewValues   map[string]any
	CreatedAt   time.Time
}

var (
	pendingEdits   = make(map[string]*PendingEdit)
	pendingEditsMu sync.RWMutex
	pendingEditTTL = 10 * time.Minute
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
	pendingEditsMu.Lock()
	defer pendingEditsMu.Unlock()
	pendingEdits[token] = edit

	// Start expiration goroutine
	go func() {
		time.Sleep(pendingEditTTL)
		pendingEditsMu.Lock()
		delete(pendingEdits, token)
		pendingEditsMu.Unlock()
	}()
	return nil
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
	delete(pendingEdits, token)
}
