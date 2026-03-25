package initial_test

import (
	"context"
	"iter"
	"testing"

	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initial"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

func TestLarkRunStatusUpdaterPatchesRootCardWhenStarted(t *testing.T) {
	patchCalls := 0
	emitted := make([]*ark_dal.ModelStreamRespReasoning, 0)
	updater := initialcore.NewLarkRunStatusUpdaterForTest(
		func(ctx context.Context, refs larkmsg.AgentStreamingCardRefs, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			patchCalls++
			if refs.MessageID != "om_pending_root" || refs.CardID != "card_pending_root" {
				t.Fatalf("unexpected refs: %+v", refs)
			}
			for item := range seq {
				emitted = append(emitted, item)
			}
			return refs, nil
		},
	)

	err := updater.MarkStarted(context.Background(), initialcore.PendingRun{
		RootTarget: initialcore.RootTarget{
			MessageID: "om_pending_root",
			CardID:    "card_pending_root",
		},
	})
	if err != nil {
		t.Fatalf("MarkStarted() error = %v", err)
	}
	if patchCalls != 1 {
		t.Fatalf("patch calls = %d, want 1", patchCalls)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted item count = %d, want 1", len(emitted))
	}
	if emitted[0].ContentStruct.Thought != "slot 已释放，开始执行。" {
		t.Fatalf("thought = %q, want start status", emitted[0].ContentStruct.Thought)
	}
	if emitted[0].ContentStruct.Reply != "已开始执行，我先处理这条任务。" {
		t.Fatalf("reply = %q, want start status", emitted[0].ContentStruct.Reply)
	}
}
