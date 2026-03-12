package carddebug

import (
	"context"
	"testing"
)

func TestResolveReceiveTarget(t *testing.T) {
	target, err := ResolveReceiveTarget("", "ou_debug_user", "")
	if err != nil {
		t.Fatalf("ResolveReceiveTarget returned error: %v", err)
	}
	if target.ReceiveIDType != "open_id" {
		t.Fatalf("expected open_id, got %q", target.ReceiveIDType)
	}
	if target.ReceiveID != "ou_debug_user" {
		t.Fatalf("unexpected receive id %q", target.ReceiveID)
	}

	target, err = ResolveReceiveTarget("", "", "oc_debug_chat")
	if err != nil {
		t.Fatalf("ResolveReceiveTarget fallback returned error: %v", err)
	}
	if target.ReceiveIDType != "chat_id" || target.ReceiveID != "oc_debug_chat" {
		t.Fatalf("unexpected fallback target: %+v", target)
	}
}

func TestResolveTemplate(t *testing.T) {
	info, ok := ResolveTemplate("NormalCardReplyTemplate")
	if !ok {
		t.Fatalf("expected template alias to resolve")
	}
	if info.ID == "" {
		t.Fatalf("expected resolved template id")
	}
}

func TestBuildSampleSpecs(t *testing.T) {
	ctx := context.Background()
	for _, spec := range []string{SpecRateLimitSample, SpecScheduleSample} {
		built, err := Build(ctx, BuildRequest{Spec: spec})
		if err != nil {
			t.Fatalf("Build(%s) returned error: %v", spec, err)
		}
		if built == nil || built.Mode != BuiltCardModeCardJSON {
			t.Fatalf("expected card json for %s, got %+v", spec, built)
		}
		if len(built.CardJSON) == 0 {
			t.Fatalf("expected non-empty card json for %s", spec)
		}
	}
}

func TestListSpecsContainsExtendedEntries(t *testing.T) {
	names := make(map[string]struct{})
	for _, spec := range ListSpecs() {
		names[spec.Name] = struct{}{}
	}
	for _, name := range []string{
		SpecScheduleList,
		SpecScheduleTask,
		SpecWordCountSample,
		SpecChunkSample,
	} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected spec %q to be listed", name)
		}
	}
}
