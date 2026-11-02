package pgstore_test

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConfirmedMigratedMessageEffectsHaveSameTaskProvenance(t *testing.T) {
	for _, name := range []string{"feishu_send_card", "feishu_send_image", "music_send_card", "music_send_audio"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			j, task, x := workTurn(t, f, "origin", "alice", name, map[string]string{"selection": "stable"})
			id := name + "-message"
			raw, _ := json.Marshal(map[string]string{"message_id": id})
			if err := f.FinishTool(ctx, j, task, x, string(raw), nil); err != nil {
				t.Fatal(err)
			}
			bound, _, err := f.TaskForMessage(ctx, task.Scope, id)
			if err != nil || bound.ID != task.ID {
				t.Fatalf("confirmed receipt not bound: %+v %v", bound, err)
			}
		})
	}
}
