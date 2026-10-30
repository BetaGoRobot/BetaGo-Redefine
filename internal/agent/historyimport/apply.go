package historyimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type HistoricalStore interface {
	AcceptHistorical(context.Context, domain.Event) (pgstore.AcceptResult, error)
	Message(context.Context, domain.Scope, string) (domain.Message, error)
	PutContext(context.Context, domain.Context) error
	Enqueue(context.Context, domain.JobSpec) (int64, error)
}

// BackfillEvent changes only transport identity and participation metadata. All
// canonical source/version/timestamps/text/media/provider facts are retained.
func BackfillEvent(input domain.Message) (domain.Event, error) {
	m := input.Clone()
	meta, err := humanMetadata(m)
	if err != nil {
		return domain.Event{}, err
	}
	meta.Kind = "backfill"
	meta.Addressed = false
	meta.Control = nil
	m.Raw, err = json.Marshal(meta)
	if err != nil {
		return domain.Event{}, fmt.Errorf("%w: canonical metadata", domain.ErrInvalid)
	}
	m.EventID = "backfill:" + digest([]any{m.Source.Scope.Key(), m.Source.MessageID, m.Source.Version, m.Deleted, bytesDigest(m.Raw)})
	e := domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: m.UpdatedAt}
	if err = e.Validate(); err != nil {
		return domain.Event{}, err
	}
	return e, nil
}

func planLedger(ledger Ledger) (Report, error) {
	report := ledgerReport(ledger)
	report.Stage = "apply"
	report.Complete = false
	var inputs []domain.Message
	for _, r := range ledger.Records {
		if r.Status != "human" {
			continue
		}
		inputs = append(inputs, r.Message.Clone())
		if _, err := BackfillEvent(*r.Message); err != nil {
			return report, err
		}
	}
	contexts, err := BuildContexts(ledger.Manifest.Scope, inputs)
	if err != nil {
		return report, err
	}
	report.Counts["planned_contexts"] = len(contexts)
	return report, nil
}

// Preview validates the entire frozen input and plans contexts without storage
// initialization, provider/model calls or service writes.
func Preview(scope domain.Scope, inventory []byte, path string) (Report, error) {
	ledger, err := ReadLedger(path, scope, inventory)
	if err != nil {
		return Report{}, err
	}
	report, err := planLedger(ledger)
	report.DryRun = true
	report.Complete = err == nil
	return report, err
}

// Apply validates all frozen records before the first database write. Each
// source transaction is idempotent. Context persistence and explicit outbox
// jobs have stable identities, so reruns repair a crash between the two without
// resetting completed jobs. Concurrent source edits are checked by PutContext
// and by existing worker validation before publication.
func Apply(ctx context.Context, store HistoricalStore, scope domain.Scope, inventory []byte, path string) (Report, error) {
	ledger, err := ReadLedger(path, scope, inventory)
	if err != nil {
		return Report{}, err
	}
	report, err := planLedger(ledger)
	if err != nil {
		return report, err
	}
	if store == nil {
		return report, fmt.Errorf("%w: historical store required", domain.ErrInvalid)
	}
	inputs := make([]domain.Message, 0, len(ledger.Records))
	for _, r := range ledger.Records {
		if r.Status != "human" {
			continue
		}
		e, err := BackfillEvent(*r.Message)
		if err != nil {
			return report, err
		}
		if ledger.Manifest.SourceType == archiveSource {
			known, readErr := store.Message(ctx, scope, r.ID)
			if readErr == nil {
				report.Counts["archive_existing_current"]++
				if known.Deleted {
					report.Counts["current_deleted"]++
					continue
				}
				if known.Source.Scope != scope || known.Source.MessageID != r.ID {
					return report, errors.New("current archive source crossed selected identity")
				}
				if _, humanErr := humanMetadata(known); humanErr != nil {
					report.Counts["current_nonhuman"]++
					continue
				}
				inputs = append(inputs, known.Clone())
				continue
			}
			if !errors.Is(readErr, domain.ErrNotFound) {
				return report, errors.New("existing archive source cannot be checked")
			}
		}
		accepted, err := store.AcceptHistorical(ctx, e)
		if err != nil {
			report.Counts["failed_sources"]++
			if errors.Is(err, domain.ErrConflict) {
				report.Counts["conflicts"]++
				return report, errors.New("historical source identity conflict")
			}
			return report, errors.New("historical source acceptance failed")
		}
		if accepted.Duplicate {
			report.Counts["duplicate"]++
		} else if accepted.Current {
			report.Counts["accepted"]++
		} else {
			report.Counts["stale"]++
		}
		// The canonical snapshot may already be older than live PG. Never derive
		// evidence from the stale input; use only the exact current human fact.
		current, err := store.Message(ctx, scope, r.ID)
		if err != nil {
			return report, errors.New("accepted current source cannot be read")
		}
		if current.Deleted {
			report.Counts["current_deleted"]++
			continue
		}
		if _, err = humanMetadata(current); err != nil {
			report.Counts["current_nonhuman"]++
			continue
		}
		if current.Source.Scope != scope || current.Source.MessageID != r.ID {
			return report, errors.New("current source crossed selected identity")
		}
		if current.Source.Version != r.Message.Source.Version {
			report.Counts["current_newer"]++
		}
		inputs = append(inputs, current.Clone())
	}
	contexts, err := BuildContexts(scope, inputs)
	if err != nil {
		return report, err
	}
	for _, c := range contexts {
		if err = store.PutContext(ctx, c); err != nil {
			report.Counts["failed_contexts"]++
			return report, errors.New("historical context persistence failed; rerun after reconciling current sources")
		}
		report.Counts["contexts"]++
		payload, _ := json.Marshal(struct {
			ID      string `json:"context_id"`
			Version int64  `json:"context_version"`
		}{c.ID, c.Version})
		for _, job := range []struct {
			kind     domain.JobKind
			priority int
		}{{domain.JobEmbedContext, 80}, {domain.JobMemory, 50}} {
			if _, err = store.Enqueue(ctx, domain.JobSpec{Kind: job.kind, Priority: job.priority, Scope: scope, Source: c.Sources[0], Payload: payload, DedupeKey: fmt.Sprintf("context:%s:%d", c.ID, c.Version)}); err != nil {
				report.Counts["failed_context_jobs"]++
				return report, errors.New("historical context continuation failed; rerun resumes stable jobs")
			}
			report.Counts[string(job.kind)]++
		}
	}
	report.Counts["context_sources"] = len(inputs)
	report.Complete = true
	return report, nil
}
