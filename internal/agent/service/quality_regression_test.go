package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

type runtimeFreshFactsModel struct {
	calls     int
	lastFacts string
	readName  string
	readArgs  string
}

func (m *runtimeFreshFactsModel) Generate(_ context.Context, req modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.calls++
	toolName, callID, args := m.readName, "quality_facts_1", m.readArgs
	if toolName == "" {
		toolName, args = "task_facts", "{}"
	}
	switch m.calls {
	case 2:
		toolName, callID, args = "quality_write", "quality_write_1", `{"amount":1}`
	case 3:
		callID = "quality_facts_2"
	case 4:
		for _, msg := range req.Messages {
			if msg.Role == "tool" && msg.ToolCallID == "quality_facts_2" {
				m.lastFacts, _ = msg.Content.(string)
			}
		}
		return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "done"}}, nil
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: callID, Type: "function", Function: modelapi.ToolFunction{Name: toolName, Arguments: args}}}}}, nil
}
func TestRuntimeQualityTaskFactsReadAfterResumeIsCurrent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"receipt":"success"}`)) }))
	defer api.Close()
	model := &runtimeFreshFactsModel{}
	reg, err := atoms.New(atoms.Options{Tasks: f.store, APIs: []atoms.APIConfig{{Name: "quality_write", URL: api.URL, Method: "POST", Parameters: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"}},"additionalProperties":false}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.Options{Store: f.store, Model: model, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	f.svc, err = service.New(service.Options{Store: f.store, Engine: eng, Messenger: f.messenger, Owner: "quality", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	m := f.event(t, "facts_request", "owner", "/bb write", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobAgentTurn)
	approvals, err := f.store.Approvals(ctx, f.scope, task.ID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals: %v %v", approvals, err)
	}
	if _, err = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: approvals[0].ID}, domain.SourceRef{}, ""); err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobAgentTurn)
	var facts struct {
		Executions []struct{ Tool, Status string }
	}
	if err = json.Unmarshal([]byte(model.lastFacts), &facts); err != nil {
		t.Fatalf("bad facts: %v", err)
	}
	for _, x := range facts.Executions {
		if x.Tool == "quality_write" && x.Status == "completed" {
			return
		}
	}
	t.Fatalf("task_facts after approved effect returned cached pre-effect facts: %s", model.lastFacts)
}

type qualityMessageReader struct {
	domain.SourceReader
	calls atomic.Int32
}

func (r *qualityMessageReader) Message(ctx context.Context, scope domain.Scope, id string) (domain.Message, error) {
	r.calls.Add(1)
	return r.SourceReader.Message(ctx, scope, id)
}

type qualityFreshRetrieval struct {
	source domain.SourceRef
	calls  atomic.Int32
}

func (r *qualityFreshRetrieval) Retrieve(_ context.Context, q domain.Query) (retrieval.Result, error) {
	if q.Scope != r.source.Scope {
		return retrieval.Result{}, domain.ErrUnauthorized
	}
	n := r.calls.Add(1)
	return retrieval.Result{Evidence: []domain.Evidence{{ID: "current", Text: "canonical material", DerivedText: fmt.Sprintf("read_%d", n), Sources: []domain.SourceRef{r.source}}}}, nil
}

func TestRuntimeQualityDynamicEvidenceReadsAgainAfterNativeResume(t *testing.T) {
	for _, name := range []string{"feishu_read_message", "search_literal", "search_discussion", "search_memory"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			material := f.event(t, "dynamic_material", "contributor", "canonical material", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
			step(t, f, domain.JobRoute)
			reader := &qualityMessageReader{SourceReader: f.store}
			search := &qualityFreshRetrieval{source: material.Source}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"receipt":"success"}`)) }))
			defer api.Close()
			model := &runtimeFreshFactsModel{readName: name, readArgs: `{"query":"material"}`}
			if name == "feishu_read_message" {
				model.readArgs = `{"message_id":"dynamic_material"}`
			}
			reg, e := atoms.New(atoms.Options{Tasks: f.store, Sources: reader, Retrieval: search, APIs: []atoms.APIConfig{{Name: "quality_write", URL: api.URL, Method: "POST", Parameters: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"}},"additionalProperties":false}`)}}})
			if e != nil {
				t.Fatal(e)
			}
			eng, e := engine.New(engine.Options{Store: f.store, Model: model, Registry: reg})
			if e != nil {
				t.Fatal(e)
			}
			f.svc, e = service.New(service.Options{Store: f.store, Engine: eng, Messenger: f.messenger, Owner: "fresh-evidence", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
			if e != nil {
				t.Fatal(e)
			}
			m := f.event(t, "dynamic_request", "owner", "/bb inspect and write", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
			step(t, f, domain.JobRoute)
			task, _, e := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
			if e != nil {
				t.Fatal(e)
			}
			step(t, f, domain.JobAgentTurn)
			approvals, e := f.store.Approvals(ctx, f.scope, task.ID)
			if e != nil || len(approvals) != 1 {
				t.Fatalf("native approval: %v %v", approvals, e)
			}
			if _, e = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: approvals[0].ID}, domain.SourceRef{}, ""); e != nil {
				t.Fatal(e)
			}
			step(t, f, domain.JobAgentTurn)
			if name == "feishu_read_message" {
				if reader.calls.Load() != 2 || !strings.Contains(model.lastFacts, "canonical material") {
					t.Fatalf("message read reused pre-resume snapshot: calls=%d result=%s", reader.calls.Load(), model.lastFacts)
				}
			} else if search.calls.Load() != 2 || !strings.Contains(model.lastFacts, "read_2") {
				t.Fatalf("search reused pre-resume evidence: calls=%d result=%s", search.calls.Load(), model.lastFacts)
			}
		})
	}
}

func TestRuntimeQualityKnownDeliveryReceiptAfterLeaseLossConverges(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "delivery_lease_request", "owner", "/bb answer", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "turn", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.QueueFinalDelivery(ctx, turn, task, "done"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteJob(ctx, turn); err != nil {
		t.Fatal(err)
	}
	first, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "old", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		ID string `json:"delivery_id"`
	}
	if err = json.Unmarshal(first.Payload, &p); err != nil {
		t.Fatal(err)
	}
	d, err := f.store.CheckDelivery(ctx, first, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.FinishDelivery(ctx, first, d, "known-success-provider-id"); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("old receipt should be audited as stale: %v", err)
	}
	// The next valid claim must reconcile the known receipt without another Send.
	before := len(f.messenger.sends)
	step(t, f, domain.JobDelivery)
	task, err = f.store.Task(ctx, f.scope, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.store.Delivery(ctx, f.scope, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.messenger.sends) != before {
		t.Fatal("known successful delivery was replayed")
	}
	if task.Status != "completed" {
		t.Fatalf("known delivered final is stranded after reclaimed job completes: task=%s delivery=%s", task.Status, got.Status)
	}
}

type qualitySender func(context.Context, domain.Scope, string, string, string, string) (string, error)

func (f qualitySender) Send(ctx context.Context, scope domain.Scope, parent, kind, content, uuid string) (string, error) {
	return f(ctx, scope, parent, kind, content, uuid)
}

func TestRuntimeQualityLateDeliveryReceiptReconcilesOnlyCurrentTask(t *testing.T) {
	for _, kind := range []string{"final", "approval"} {
		for _, change := range []string{"unchanged", "cancel", "modify"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				f := setup(t)
				ctx := context.Background()
				m := f.event(t, "late_request", "owner", "/bb answer", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
				step(t, f, domain.JobRoute)
				task, _, e := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
				if e != nil {
					t.Fatal(e)
				}
				if kind == "approval" {
					step(t, f, domain.JobAgentTurn)
				} else {
					j, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "queue", time.Minute)
					if e != nil {
						t.Fatal(e)
					}
					if e = f.store.QueueFinalDelivery(ctx, j, task, "done"); e != nil {
						t.Fatal(e)
					}
					if e = f.store.CompleteJob(ctx, j); e != nil {
						t.Fatal(e)
					}
				}
				var id string
				if e = f.db.QueryRow(`SELECT delivery_id FROM "`+f.schema+`".deliveries WHERE task_id=$1`, task.ID).Scan(&id); e != nil {
					t.Fatal(e)
				}
				sends := 0
				sender := qualitySender(func(_ context.Context, _ domain.Scope, _, _, _, _ string) (string, error) {
					sends++
					_, e := f.db.Exec(`UPDATE "` + f.schema + `".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE kind='delivery' AND status='running'`)
					return "known-late-receipt", e
				})
				f.svc, e = service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: sender, Owner: "late", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
				if e != nil {
					t.Fatal(e)
				}
				if e = f.svc.Step(ctx, domain.JobDelivery); !errors.Is(e, domain.ErrStaleClaim) {
					t.Fatalf("expected lease loss during successful Send: %v", e)
				}
				d, e := f.store.Delivery(ctx, f.scope, id)
				if e != nil || d.Status != "delivered_late" || d.MessageID != "known-late-receipt" {
					t.Fatalf("known receipt not audited: %+v %v", d, e)
				}
				if change != "unchanged" {
					var source domain.SourceRef
					if change == "modify" {
						source = f.event(t, "new_parameters", "owner", "new parameters", "", domain.IngressMetadata{Kind: "message", SenderType: "user"}).Source
					}
					if _, e = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: change, TaskID: task.ID, Revision: task.Revision}, source, "new parameters"); e != nil {
						t.Fatal(e)
					}
				}
				beforeModel, beforeTools := f.modelCalls.Load(), f.toolCalls.Load()
				step(t, f, domain.JobDelivery)
				d, e = f.store.Delivery(ctx, f.scope, id)
				if e != nil {
					t.Fatal(e)
				}
				task, e = f.store.Task(ctx, f.scope, task.ID)
				if e != nil {
					t.Fatal(e)
				}
				if sends != 1 || f.modelCalls.Load() != beforeModel || f.toolCalls.Load() != beforeTools {
					t.Fatalf("receipt reconciliation replayed Send/model/tool: sends=%d", sends)
				}
				wantDelivery, wantTask := "delivered", "completed"
				if kind == "approval" {
					wantTask = "waiting"
				}
				if change == "cancel" {
					wantDelivery, wantTask = "delivered_late", "canceled"
				} else if change == "modify" {
					wantDelivery, wantTask = "delivered_late", "ready"
				}
				if d.Status != wantDelivery || task.Status != wantTask {
					t.Fatalf("receipt advanced wrong lifecycle: delivery=%s task=%s", d.Status, task.Status)
				}
			})
		}
	}
}

type sameTurnFactsModel struct {
	calls     int
	lastFacts string
}

func (m *sameTurnFactsModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.calls++
	if m.calls <= 2 {
		return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: fmt.Sprintf("same_read_%d", m.calls), Type: "function", Function: modelapi.ToolFunction{Name: "task_facts", Arguments: `{}`}}}}}, nil
	}
	for _, msg := range r.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "same_read_2" {
			m.lastFacts, _ = msg.Content.(string)
		}
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "done"}}, nil
}
func TestRuntimeQualityTaskFactsReadsFreshWithinSameTurnAndFence(t *testing.T) {
	f := setup(t)
	model := &sameTurnFactsModel{}
	reg, e := atoms.New(atoms.Options{Tasks: f.store})
	if e != nil {
		t.Fatal(e)
	}
	eng, e := engine.New(engine.Options{Store: f.store, Model: model, Registry: reg})
	if e != nil {
		t.Fatal(e)
	}
	f.svc, e = service.New(service.Options{Store: f.store, Engine: eng, Messenger: f.messenger, Owner: "same-turn", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	f.event(t, "same_turn_facts", "owner", "/bb status", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	step(t, f, domain.JobAgentTurn)
	var facts struct {
		Executions []struct{ Tool, Status string }
	}
	if e = json.Unmarshal([]byte(model.lastFacts), &facts); e != nil {
		t.Fatal(e)
	}
	completed, running := 0, 0
	for _, x := range facts.Executions {
		if x.Tool == "task_facts" {
			if x.Status == "completed" {
				completed++
			}
			if x.Status == "running_read" {
				running++
			}
		}
	}
	if completed != 1 || running != 1 {
		t.Fatalf("same-turn read reused old snapshot: %s", model.lastFacts)
	}
}

func TestRuntimeQualityNewSenderFinishesAfterOldFenceReceipt(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := f.event(t, "interleaved_delivery_root", "owner", "/bb answer", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "queue", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.QueueFinalDelivery(ctx, turn, task, "done"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteJob(ctx, turn); err != nil {
		t.Fatal(err)
	}
	startedOld, startedNew := make(chan struct{}), make(chan struct{})
	releaseOld, releaseNew := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	sender := qualitySender(func(c context.Context, _ domain.Scope, _, _, _, _ string) (string, error) {
		n := calls.Add(1)
		var release <-chan struct{}
		if n == 1 {
			close(startedOld)
			release = releaseOld
		} else if n == 2 {
			close(startedNew)
			release = releaseNew
		} else {
			return "", errors.New("unexpected replay")
		}
		select {
		case <-release:
			return "same-idempotent-provider-receipt", nil
		case <-c.Done():
			return "", c.Err()
		}
	})
	makeService := func(owner string) *service.Service {
		svc, e := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: sender, Owner: owner, Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
		if e != nil {
			t.Fatal(e)
		}
		return svc
	}
	oldSvc, newSvc := makeService("old-sender"), makeService("new-sender")
	oldDone, newDone := make(chan error, 1), make(chan error, 1)
	go func() { oldDone <- oldSvc.Step(ctx, domain.JobDelivery) }()
	select {
	case <-startedOld:
	case <-ctx.Done():
		t.Fatal("old Send did not start")
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE kind='delivery' AND status='running'`); err != nil {
		t.Fatal(err)
	}
	go func() { newDone <- newSvc.Step(ctx, domain.JobDelivery) }()
	select {
	case <-startedNew:
	case <-ctx.Done():
		t.Fatal("new fence Send did not start")
	}
	// New CheckDelivery saw pending. Now the previous Send succeeds before the
	// current Send returns, so current FinishDelivery sees the late receipt.
	close(releaseOld)
	if err = <-oldDone; !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("old completion: %v", err)
	}
	close(releaseNew)
	if err = <-newDone; err != nil {
		t.Fatalf("current completion: %v", err)
	}
	task, err = f.store.Task(ctx, f.scope, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var deliveryStatus, jobStatus string
	if err = f.db.QueryRow(`SELECT d.status,j.status FROM "`+f.schema+`".deliveries d JOIN "`+f.schema+`".jobs j ON j.kind='delivery' WHERE d.task_id=$1`, task.ID).Scan(&deliveryStatus, &jobStatus); err != nil {
		t.Fatal(err)
	}
	if task.Status != "completed" || deliveryStatus != "delivered" || jobStatus != "completed" {
		t.Fatalf("current successful sender failed to adopt older known receipt: task=%s delivery=%s job=%s sends=%d", task.Status, deliveryStatus, jobStatus, calls.Load())
	}
}

func TestRuntimeQualityInterleavedReceiptsPreserveTaskFences(t *testing.T) {
	for _, kind := range []string{"final", "approval"} {
		for _, change := range []string{"unchanged", "new_first", "cancel", "modify", "source_edit", "lease", "deadline"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				f := setup(t)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				root := f.event(t, "concurrent_delivery_root", "owner", "/bb answer", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
				step(t, f, domain.JobRoute)
				task, _, e := f.store.TaskForMessage(ctx, f.scope, root.Source.MessageID)
				if e != nil {
					t.Fatal(e)
				}
				if kind == "approval" {
					step(t, f, domain.JobAgentTurn)
				} else {
					j, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "queue", time.Minute)
					if e != nil {
						t.Fatal(e)
					}
					if e = f.store.QueueFinalDelivery(ctx, j, task, "done"); e != nil {
						t.Fatal(e)
					}
					if e = f.store.CompleteJob(ctx, j); e != nil {
						t.Fatal(e)
					}
				}
				var id string
				if e = f.db.QueryRow(`SELECT delivery_id FROM "`+f.schema+`".deliveries WHERE task_id=$1`, task.ID).Scan(&id); e != nil {
					t.Fatal(e)
				}
				started := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
				release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
				var calls atomic.Int32
				var uuids [2]string
				sender := qualitySender(func(ctx context.Context, _ domain.Scope, _, _, _, uuid string) (string, error) {
					n := int(calls.Add(1)) - 1
					if n >= 2 {
						return "", errors.New("unexpected third Send")
					}
					uuids[n] = uuid
					close(started[n])
					select {
					case <-release[n]:
						return "same-idempotent-provider-receipt", nil
					case <-ctx.Done():
						return "", ctx.Err()
					}
				})
				makeService := func(owner string) *service.Service {
					svc, e := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: sender, Owner: owner, Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour})
					if e != nil {
						t.Fatal(e)
					}
					return svc
				}
				waitStarted := func(n int) {
					t.Helper()
					select {
					case <-started[n]:
					case <-ctx.Done():
						t.Fatal("Send did not start")
					}
				}
				oldDone, newDone := make(chan error, 1), make(chan error, 1)
				oldSvc, newSvc := makeService("old"), makeService("new")
				go func() { oldDone <- oldSvc.Step(ctx, domain.JobDelivery) }()
				waitStarted(0)
				if _, e = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE kind='delivery' AND status='running'`); e != nil {
					t.Fatal(e)
				}
				go func() { newDone <- newSvc.Step(ctx, domain.JobDelivery) }()
				waitStarted(1)
				if change == "new_first" {
					close(release[1])
					if e = <-newDone; e != nil {
						t.Fatal(e)
					}
				}
				close(release[0])
				if e = <-oldDone; !errors.Is(e, domain.ErrStaleClaim) {
					t.Fatalf("old completion: %v", e)
				}
				e = nil
				switch change {
				case "cancel", "modify":
					var source domain.SourceRef
					if change == "modify" {
						source = f.event(t, "concurrent_modified", "owner", "new parameters", "", domain.IngressMetadata{Kind: "message", SenderType: "user"}).Source
					}
					_, e = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: change, TaskID: task.ID, Revision: task.Revision}, source, "new parameters")
				case "source_edit":
					root.Source.Version++
					root.EventID = "concurrent_source_edited"
					root.Text = "edited request"
					root.UpdatedAt = root.UpdatedAt.Add(time.Second)
					root.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
					_, e = f.store.Accept(ctx, domain.Event{ID: root.EventID, Type: domain.EventMessage, Message: root, OccurredAt: root.UpdatedAt})
				case "lease":
					_, e = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE kind='delivery' AND status='running'`)
				case "deadline":
					_, e = f.db.Exec(`UPDATE "`+f.schema+`".tasks SET expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID)
				}
				if e != nil {
					t.Fatal(e)
				}
				if change != "new_first" {
					close(release[1])
					e = <-newDone
					if change == "lease" {
						if !errors.Is(e, domain.ErrStaleClaim) {
							t.Fatalf("expired new claim: %v", e)
						}
					} else if e != nil {
						t.Fatal(e)
					}
				}
				d, e := f.store.Delivery(ctx, f.scope, id)
				if e != nil {
					t.Fatal(e)
				}
				task, e = f.store.Task(ctx, f.scope, task.ID)
				if e != nil {
					t.Fatal(e)
				}
				wantDelivery, wantTask := "delivered_late", "ready_delivery"
				if kind == "approval" {
					wantTask = "waiting"
				}
				switch change {
				case "unchanged", "new_first":
					wantDelivery = "delivered"
					if kind == "final" {
						wantTask = "completed"
					}
				case "cancel":
					wantTask = "canceled"
				case "modify":
					wantTask = "ready"
				}
				if d.Status != wantDelivery || task.Status != wantTask || d.MessageID != "same-idempotent-provider-receipt" {
					t.Fatalf("interleaved receipt advanced wrong state: delivery=%s task=%s receipt=%s", d.Status, task.Status, d.MessageID)
				}
				if calls.Load() != 2 || uuids[0] == "" || uuids[0] != uuids[1] {
					t.Fatalf("concurrent Send did not use stable UUID: count=%d uuids=%v", calls.Load(), uuids)
				}
			})
		}
	}
}
