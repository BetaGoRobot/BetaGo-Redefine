package utils

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestGetIfInthreadRespectsSceneDefaultWhenThreadingDisabled(t *testing.T) {
	meta := &xhandler.BaseMetaData{}

	if GetIfInthread(context.Background(), meta, false) {
		t.Fatal("expected false scene default to stay out of thread")
	}
}

func TestGetIfInthreadKeepsP2POutOfThread(t *testing.T) {
	meta := &xhandler.BaseMetaData{IsP2P: true}

	if GetIfInthread(context.Background(), meta, true) {
		t.Fatal("expected p2p replies to stay out of thread")
	}
}

func TestGetIfInthreadKeepsSceneDefault(t *testing.T) {
	meta := &xhandler.BaseMetaData{}

	if GetIfInthread(context.Background(), meta, false) {
		t.Fatal("expected false scene default to stay out of thread")
	}
	if !GetIfInthread(context.Background(), meta, true) {
		t.Fatal("expected true scene default to stay in thread")
	}
}
