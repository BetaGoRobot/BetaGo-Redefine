package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

var ErrLimit = errors.New("media processing limit exceeded")
var ErrModalityUnavailable = errors.New("media modality not configured")

type Limits struct {
	MaxDownloadBytes, MaxZipBytes, MaxTextBytes, MaxOutputBytes int64
	MaxZipEntries, MaxPages, MaxFrames, MaxPixels               int
	MaxDuration, Timeout                                        time.Duration
}

func normalizeLimits(l Limits) (Limits, error) {
	if l.MaxDownloadBytes == 0 {
		l.MaxDownloadBytes = 32 << 20
	}
	if l.MaxZipBytes == 0 {
		l.MaxZipBytes = 16 << 20
	}
	if l.MaxTextBytes == 0 {
		l.MaxTextBytes = 1 << 20
	}
	if l.MaxOutputBytes == 0 {
		l.MaxOutputBytes = 16 << 20
	}
	if l.MaxZipEntries == 0 {
		l.MaxZipEntries = 512
	}
	if l.MaxPages == 0 {
		l.MaxPages = 32
	}
	if l.MaxFrames == 0 {
		l.MaxFrames = 8
	}
	if l.MaxPixels == 0 {
		l.MaxPixels = 25_000_000
	}
	if l.MaxDuration == 0 {
		l.MaxDuration = 5 * time.Minute
	}
	if l.Timeout == 0 {
		l.Timeout = 60 * time.Second
	}
	if l.MaxDownloadBytes < 1 || l.MaxDownloadBytes > 256<<20 || l.MaxZipBytes < 1 || l.MaxZipBytes > 256<<20 || l.MaxTextBytes < 1 || l.MaxTextBytes > 8<<20 || l.MaxOutputBytes < 1 || l.MaxOutputBytes > 64<<20 || l.MaxZipEntries < 1 || l.MaxZipEntries > 4096 || l.MaxPages < 1 || l.MaxPages > 256 || l.MaxFrames < 1 || l.MaxFrames > 64 || l.MaxPixels < 1 || l.MaxPixels > 100_000_000 || l.MaxDuration <= 0 || l.MaxDuration > time.Hour || l.Timeout <= 0 || l.Timeout > 5*time.Minute {
		return l, ErrLimit
	}
	return l, nil
}
func boundedRead(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(b)) > max {
		return nil, ErrLimit
	}
	return b, err
}

type outputBuffer struct {
	b        bytes.Buffer
	limit    int64
	cancel   context.CancelFunc
	exceeded bool
}

func (w *outputBuffer) Write(p []byte) (int, error) {
	if int64(len(p))+int64(w.b.Len()) > w.limit {
		w.exceeded = true
		w.cancel()
		return 0, ErrLimit
	}
	return w.b.Write(p)
}

// Parser processes are killed as a group on cancellation, including descendants
// that inherited pipes. Neither stderr nor command arguments enter errors.
func run(ctx context.Context, timeout time.Duration, max int64, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	w := &outputBuffer{limit: max, cancel: cancel}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	err := cmd.Run()
	if w.exceeded {
		return nil, ErrLimit
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("media parser failed")
	}
	return w.b.Bytes(), nil
}
