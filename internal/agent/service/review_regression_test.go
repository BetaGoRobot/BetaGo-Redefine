package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestRuntimeReviewRegressionRevokedPolicyMustRejectApprovalEnqueue(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "policy_request", "owner", "/bb write", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobAgentTurn)
	as, err := f.store.Approvals(ctx, f.scope, task.ID)
	if err != nil || len(as) != 1 {
		t.Fatalf("approval: %v %v", as, err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='business_write'`); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}, domain.SourceRef{}, "")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked policy accepted approval and queued resume: %v", err)
	}
}

func TestRuntimeReviewRegressionChangedContextMustPermitOwnerReplanAndCancel(t *testing.T) {
	for _, action := range []string{"modify", "cancel"} {
		t.Run(action, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			evidence := f.event(t, "context_evidence", "other", "old fact", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
			step(t, f, domain.JobRoute)
			m := f.event(t, "context_request", "owner", "/bb write", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
			step(t, f, domain.JobRoute)
			task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			step(t, f, domain.JobAgentTurn)
			evidence.Source.Version = 2
			evidence.EventID = "context_edited"
			evidence.Text = "corrected fact"
			evidence.UpdatedAt = evidence.UpdatedAt.Add(time.Second)
			if _, err = f.store.Accept(ctx, domain.Event{ID: evidence.EventID, Type: domain.EventMessage, Message: evidence, OccurredAt: evidence.UpdatedAt}); err != nil {
				t.Fatal(err)
			}
			follow := f.event(t, "context_followup", "owner", "use corrected material", m.Source.MessageID, domain.IngressMetadata{Kind: "message", SenderType: "user"})
			_, err = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: action, TaskID: task.ID, Revision: 1}, follow.Source, follow.Text)
			if err != nil {
				t.Fatalf("owner %s cannot recover from stale context: %v", action, err)
			}
		})
	}
}

func TestRuntimeReviewRegressionReplyToCrossMemberImageBindsMedia(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	image := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "other_image", AuthorID: "contributor", Version: 1, OccurredAt: now}, EventID: "other_image", Raw: raw, UpdatedAt: now, Media: []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "synthetic-image"}}}
	if _, err := f.store.Accept(ctx, domain.Event{ID: image.EventID, Type: domain.EventMessage, Message: image, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobRoute)
	mediaJob, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "media", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutMedia(ctx, mediaJob, domain.MediaArtifact{Source: image.Source, Parts: []domain.MediaPart{{Text: "CROSS_MEMBER_DERIVED_427", Page: 3}}}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteJob(ctx, mediaJob); err != nil {
		t.Fatal(err)
	}
	f.event(t, "image_question", "owner", "/bb describe this picture", image.Source.MessageID, domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	step(t, f, domain.JobAgentTurn)
	prompt := <-f.modelPrompts
	if !strings.Contains(prompt, "CROSS_MEMBER_DERIVED_427") {
		t.Fatalf("explicitly referenced cross-member image omitted from native prompt")
	}
}

func TestRuntimeReviewRegressionReplyToCompletedToolBotMessageStartsTask(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "toolbot_request", "owner", "/bb initial", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".task_sources(scope_key,task_id,message_id) VALUES($1,$2,'toolbot_message')`, f.scope.Key(), task.ID); err != nil {
		t.Fatal(err)
	}
	j, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "complete", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.QueueFinalDelivery(ctx, j, task, "done"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobDelivery)
	reply := f.event(t, "toolbot_followup", "second", "next question", "toolbot_message", domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	if _, _, err = f.store.TaskForMessage(ctx, f.scope, reply.Source.MessageID); err != nil {
		t.Fatalf("reply to PG-bound bot tool message was ignored: %v", err)
	}
}

func TestRuntimeReviewRegressionOldProposalReplyCannotModifyCurrentRevision(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.event(t, "stale_proposal_root", "owner", "/bb initial", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, root.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobAgentTurn)
	step(t, f, domain.JobDelivery)
	f.messenger.mu.Lock()
	oldProposal := "out_" + f.messenger.sends[0].uuid
	f.messenger.mu.Unlock()
	modified := f.event(t, "stale_proposal_current", "owner", "current version request", root.Source.MessageID, domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	task, err = f.store.Task(ctx, f.scope, task.ID)
	if err != nil || task.Revision != 2 {
		t.Fatalf("modify baseline: %+v %v", task, err)
	}
	_ = modified
	f.event(t, "stale_proposal_reply", "owner", "change old parameters to 3", oldProposal, domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	task, err = f.store.Task(ctx, f.scope, task.ID)
	if err != nil || task.Revision != 2 {
		t.Fatalf("old revision proposal reply mutated current task: revision=%d request=%q err=%v", task.Revision, task.Request, err)
	}
}

func TestRuntimeReviewRegressionOwnerCanExplicitlyDelegateWithoutPriorContribution(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.event(t, "delegate_root", "owner", "/bb initial", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, root.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	f.event(t, "delegate_action", "owner", "/delegate @_user_1", root.Source.MessageID, domain.IngressMetadata{Kind: "message", SenderType: "user", Mentions: []domain.IngressMention{{Key: "@_user_1", OpenID: "explicitly-selected-member", Name: "Selected member"}}})
	step(t, f, domain.JobRoute)
	task, err = f.store.Task(ctx, f.scope, task.ID)
	if err != nil || !task.Authorized("explicitly-selected-member") {
		t.Fatalf("verified owner mention did not grant explicit delegation without prior contribution: %+v %v", task.Delegates, err)
	}
}

func TestRuntimeReviewMissingExplicitParentBackfillsThenWaitsForMedia(t *testing.T) {
	ctx := context.Background()
	var f fixture
	resolutions := 0
	var material domain.Message
	f = setupRuntime(t, nil, nil, func(ctx context.Context, scope domain.Scope, id string) error {
		resolutions++
		if scope != f.scope || id != "pre_cutover_image" {
			t.Errorf("unscoped/unrelated resolver input: %+v %s", scope, id)
		}
		now := time.Now().UTC().Add(-time.Hour)
		raw, _ := json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
		material = domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: id, AuthorID: "historical-author", Version: 1, OccurredAt: now}, EventID: "refreshed_historical", UpdatedAt: now, Raw: raw, Media: []domain.Media{{Type: domain.MediaImage, ResourceID: "historical-image"}}}
		_, e := f.store.Accept(ctx, domain.Event{ID: material.EventID, Type: domain.EventMessage, Message: material, OccurredAt: now})
		return e
	})
	unrelatedAt := time.Now().UTC().Add(-2 * time.Hour)
	unrelatedRaw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	unrelated := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "unrelated_image", AuthorID: "unrelated-author", Version: 1, OccurredAt: unrelatedAt}, EventID: "unrelated_image", UpdatedAt: unrelatedAt, Raw: unrelatedRaw, Media: []domain.Media{{Type: domain.MediaImage, ResourceID: "unrelated-resource"}}}
	if _, e := f.store.Accept(ctx, domain.Event{ID: unrelated.EventID, Type: domain.EventMessage, Message: unrelated, OccurredAt: unrelatedAt}); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobRoute)
	unrelatedJob, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "unrelated-media", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.store.PutMedia(ctx, unrelatedJob, domain.MediaArtifact{Source: unrelated.Source, Parts: []domain.MediaPart{{Text: "UNRELATED_DERIVED_MUST_NOT_LEAK"}}}); e != nil {
		t.Fatal(e)
	}
	f.store.CompleteJob(ctx, unrelatedJob)
	f.event(t, "ordinary_before_cutover", "other", "ordinary chat", "unrelated_missing_parent", domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	if resolutions != 0 {
		t.Fatal("ordinary chat resolved historical material")
	}
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	root := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "historical_question", AuthorID: "requester", Version: 1, OccurredAt: now}, EventID: "historical_question", Text: "/bb describe that image", ParentID: "pre_cutover_image", RootID: "pre_cutover_image", ThreadID: "omt_not_a_message", UpdatedAt: now, Raw: raw}
	if _, e := f.store.Accept(ctx, domain.Event{ID: root.EventID, Type: domain.EventMessage, Message: root, OccurredAt: now}); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobRoute)
	step(t, f, domain.JobAgentTurn)
	if resolutions != 1 || f.modelCalls.Load() != 0 {
		t.Fatalf("missing media did not wait after scoped backfill: resolver=%d model=%d", resolutions, f.modelCalls.Load())
	}
	var attempt int
	var status string
	if e := f.db.QueryRow(`SELECT attempt,status FROM "`+f.schema+`".jobs WHERE kind='agent_turn'`).Scan(&attempt, &status); e != nil || attempt != 0 || status != "pending" {
		t.Fatalf("material wait burned attempt: %d %s %v", attempt, status, e)
	}
	mediaJob, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "historical-media", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.store.PutMedia(ctx, mediaJob, domain.MediaArtifact{Source: material.Source, Parts: []domain.MediaPart{{Text: "BACKFILLED_DERIVED_427", Page: 4, Start: 5 * time.Second, End: 6 * time.Second}}}); e != nil {
		t.Fatal(e)
	}
	f.store.CompleteJob(ctx, mediaJob)
	if _, e = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='agent_turn'`); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobAgentTurn)
	prompt := <-f.modelPrompts
	if strings.Contains(prompt, "UNRELATED_DERIVED_MUST_NOT_LEAK") {
		t.Fatal("unrelated group media entered task materials")
	}
	for _, want := range []string{"BACKFILLED_DERIVED_427", "historical-author", `\"Page\":4`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("task material provenance missing %q", want)
		}
	}
	if resolutions != 1 {
		t.Fatal("material resolver reran after PG backfill")
	}
	task, _, e := f.store.TaskForMessage(ctx, f.scope, root.Source.MessageID)
	if e != nil || task.OwnerID != "requester" || task.RequesterID != "requester" {
		t.Fatalf("material author replaced requester: %+v %v", task, e)
	}
	as, _ := f.store.Approvals(ctx, f.scope, task.ID)
	if len(as) != 1 {
		t.Fatal("expected native approval")
	}
	if _, e = f.store.ControlTask(ctx, f.scope, "historical-author", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}, domain.SourceRef{}, ""); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("material author gained approval authority: %v", e)
	}
}

func TestRuntimeQualityRestartRetainsRevokedProposal(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "restart_policy_request", "owner", "/bb write", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobAgentTurn)
	approvals, err := f.store.Approvals(ctx, f.scope, task.ID)
	if err != nil || len(approvals) != 1 {
		t.Fatal("approval fixture failed")
	}
	rows, err := f.db.Query(`SELECT tool_name,side_effect,policy_digest FROM "` + f.schema + `".tool_policies ORDER BY tool_name`)
	if err != nil {
		t.Fatal(err)
	}
	var policies []pgstore.ToolPolicy
	for rows.Next() {
		var p pgstore.ToolPolicy
		if err = rows.Scan(&p.Name, &p.SideEffect, &p.Digest); err != nil {
			t.Fatal(err)
		}
		policies = append(policies, p)
	}
	rows.Close()
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='business_write'`); err != nil {
		t.Fatal(err)
	}
	control := domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: approvals[0].ID}
	if _, err = f.store.ControlTask(ctx, f.scope, "owner", control, domain.SourceRef{}, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revocation fixture: %v", err)
	}
	// Engine.New calls exactly this synchronization on every process start.
	if err = f.store.SyncToolPolicies(ctx, policies); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ControlTask(ctx, f.scope, "owner", control, domain.SourceRef{}, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("restart reenabled revoked grant and accepted existing proposal: %v", err)
	}
}
