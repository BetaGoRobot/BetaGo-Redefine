package cardaction

import (
	"context"
	"testing"
)

func TestRunAsyncTaskRecoversPanic(t *testing.T) {
	completed := false

	func() {
		defer func() { completed = true }()
		runAsyncTask(context.Background(), "command.refresh", func(context.Context) {
			panic("malformed refresh event")
		})
	}()

	if !completed {
		t.Fatal("async card action panic escaped its goroutine boundary")
	}
}
