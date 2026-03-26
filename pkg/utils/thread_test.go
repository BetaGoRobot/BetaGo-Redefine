package utils

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestGetIfInthreadForcesThreadInAgenticMode(t *testing.T) {
	meta := &xhandler.BaseMetaData{}
	meta.SetExtra("interaction_mode", "agentic")

	if !GetIfInthread(context.Background(), meta, false) {
		t.Fatal("expected agentic mode to force in-thread reply")
	}
}

func TestGetIfInthreadKeepsP2POutOfThreadInAgenticMode(t *testing.T) {
	meta := &xhandler.BaseMetaData{IsP2P: true}
	meta.SetExtra("interaction_mode", "agentic")

	if GetIfInthread(context.Background(), meta, true) {
		t.Fatal("expected p2p replies to stay out of thread")
	}
}

func TestGetIfInthreadKeepsSceneDefaultOutsideAgenticMode(t *testing.T) {
	meta := &xhandler.BaseMetaData{}
	meta.SetExtra("interaction_mode", "standard")

	if GetIfInthread(context.Background(), meta, false) {
		t.Fatal("expected standard mode with false scene default to stay out of thread")
	}
	if !GetIfInthread(context.Background(), meta, true) {
		t.Fatal("expected standard mode with true scene default to stay in thread")
	}
}
