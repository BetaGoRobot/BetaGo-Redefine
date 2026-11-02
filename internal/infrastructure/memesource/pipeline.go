package memesource

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

type pipelineJob struct {
	index  int
	source Source
}

type pipelineEntry struct {
	source  Source
	cacheID string
	outcome sourceOutcome
	ready   bool
}

type pipelineCatalog struct {
	catalog Catalog
	err     error
}

// SyncFromWiki overlaps public detail fetching with model encoding. All database
// operations and final records remain on one ordered coordinator. This streaming
// entry point publishes only a completely traversed, error-free catalog; Sync
// remains available for deliberately bounded, already-fetched catalogs.
func SyncFromWiki(ctx context.Context, wiki *WikiClient, fetch CatalogOptions, deps SyncDependencies, options SyncOptions) (report SyncReport, err error) {
	started := time.Now()
	defer func() { report.ElapsedMS = time.Since(started).Milliseconds() }()
	report = SyncReport{Generation: options.Generation, Errors: []SourceError{}, Calls: []memes.CallMetadata{}}
	if wiki == nil || deps.Store == nil || deps.Model == nil || deps.Publisher == nil || strings.TrimSpace(options.ModelID) == "" {
		return report, errors.New("Wiki pipeline requires client, store, model, publisher and model ID")
	}
	if options.SourceWorkers == 0 {
		options.SourceWorkers = 1
	}
	if options.SourceWorkers < 1 || options.SourceWorkers > 64 {
		return report, errors.New("source workers must be 1..64")
	}
	prompt, schema, encodingHash, contractErr := encodingContract(options)
	if contractErr != nil {
		return report, contractErr
	}
	workCtx, cancel := context.WithCancel(ctx)
	sources := make(chan Source)
	finished := make(chan pipelineCatalog, 1)
	jobs := make(chan pipelineJob)
	results := make(chan sourceOutcome, options.SourceWorkers)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		fetch.sourceSink = func(source Source) error {
			select {
			case sources <- source:
				return nil
			case <-workCtx.Done():
				return workCtx.Err()
			}
		}
		catalog, fetchErr := wiki.Catalog(workCtx, fetch)
		close(sources)
		finished <- pipelineCatalog{catalog: catalog, err: fetchErr}
	}()
	for worker := 0; worker < options.SourceWorkers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-workCtx.Done():
					return
				case job := <-jobs:
					if workCtx.Err() != nil {
						return
					}
					outcome := encodeSource(workCtx, job.source, deps.Model, prompt, schema, encodingHash)
					outcome.index = job.index
					select {
					case results <- outcome:
					case <-workCtx.Done():
						return
					}
				}
			}
		}()
	}
	defer func() { cancel(); workers.Wait() }()
	entries := []pipelineEntry{}
	pending := []pipelineJob{}
	all := []memes.IndexRecord{}
	seen := map[string]bool{}
	next, inFlight := 0, 0
	fetchDone := false
	var fetchErr error
	emit := func(phase, sourceID string, failures ...*SourceError) {
		var failure *SourceError
		if len(failures) > 0 {
			failure = failures[0]
		}
		if options.Progress != nil {
			total := fetch.Limit
			if fetchDone {
				total = report.SourceCount
			}
			options.Progress(SyncProgress{Phase: phase, SourceID: sourceID, Failure: failure, Completed: next, Total: total, Fetched: len(entries), CatalogComplete: report.CatalogComplete, EncodedSources: report.EncodedSources, ReusedSources: report.ReusedSources, Errors: len(report.Errors), CardCount: len(all)})
		}
	}
	flush := func() error {
		for next < len(entries) && entries[next].ready {
			entry := &entries[next]
			report.Calls = append(report.Calls, entry.outcome.calls...)
			if entry.outcome.failure != nil {
				report.Errors = append(report.Errors, *entry.outcome.failure)
			} else {
				if entry.outcome.reused {
					report.ReusedSources++
				} else {
					if _, putErr := deps.Store.Put(ctx, memestore.Card, entry.cacheID, entry.outcome.saved); putErr != nil {
						return ErrStorage
					}
					report.EncodedSources++
				}
				for _, record := range entry.outcome.saved.Records {
					if seen[record.UseID] {
						report.Errors = append(report.Errors, SourceError{SourceID: entry.source.ID, Stage: "compile", Error: "duplicate use_id across source snapshots"})
						continue
					}
					seen[record.UseID] = true
					all = append(all, record)
				}
			}
			sourceID := entry.source.ID
			next++
			emit("source", sourceID, entry.outcome.failure)
		}
		return nil
	}
	for !fetchDone || len(pending) > 0 || inFlight > 0 {
		var send chan pipelineJob
		var job pipelineJob
		if len(pending) > 0 {
			send = jobs
			job = pending[0]
		}
		receive := sources
		if len(pending) >= 2*options.SourceWorkers {
			receive = nil
		}
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		case source, open := <-receive:
			if !open {
				sources = nil
				continue
			}
			i := len(entries)
			entry := pipelineEntry{source: source, cacheID: "wiki_encoding:" + source.Revision + ":" + encodingHash}
			copy := source
			copy.Revision = ""
			revision, fpErr := memes.Fingerprint(copy)
			if fpErr != nil || source.Revision == "" || revision != source.Revision {
				entry.ready = true
				entry.outcome.failure = &SourceError{SourceID: source.ID, Stage: "source", Error: "source revision does not match immutable content"}
			} else {
				if _, putErr := deps.Store.Put(ctx, memestore.Source, source.StorageID(), source); putErr != nil {
					return report, ErrStorage
				}
				saved, found, cacheErr := loadEncoding(ctx, deps.Store, entry.cacheID, source, encodingHash)
				if cacheErr != nil {
					return report, cacheErr
				}
				if found {
					entry.ready = true
					entry.outcome = sourceOutcome{index: i, saved: saved, reused: true}
				} else {
					pending = append(pending, pipelineJob{index: i, source: source})
				}
			}
			entries = append(entries, entry)
			if !fetchDone {
				report.SourceCount = len(entries)
			}
			if err = flush(); err != nil {
				return report, err
			}
		case send <- job:
			pending = pending[1:]
			inFlight++
		case outcome := <-results:
			inFlight--
			entries[outcome.index].outcome = outcome
			entries[outcome.index].ready = true
			if err = flush(); err != nil {
				return report, err
			}
		case end := <-finished:
			fetchDone = true
			finished = nil
			fetchErr = end.err
			report.CatalogComplete = end.catalog.Complete
			report.HasMore = end.catalog.HasMore
			report.NextOffset = end.catalog.NextOffset
			report.Errors = append(report.Errors, end.catalog.Errors...)
			report.SourceCount = len(end.catalog.Sources) + len(end.catalog.Errors)
			if fetchErr != nil {
				report.Errors = append(report.Errors, SourceError{SourceID: "wiki:catalog", Stage: "catalog", Error: fetchErr.Error()})
				report.SourceCount++
			}
			emit("catalog", "")
		}
	}
	report.CardCount = len(all)
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if fetchErr != nil {
		return report, fetchErr
	}
	if !report.CatalogComplete || report.HasMore || next != len(entries) || len(report.Errors) > 0 || len(all) == 0 {
		return report, ErrIncomplete
	}
	if options.Generation == "" {
		digest, _ := memes.Fingerprint(all)
		options.Generation = "wd_" + digest[:12]
	}
	report.Generation = options.Generation
	emit("publish", "")
	manifest, publishErr := deps.Publisher.Publish(ctx, options.Generation, all)
	if publishErr != nil {
		return report, ErrPublication
	}
	if !manifest.Verified || manifest.Generation != options.Generation || manifest.RecordCount != len(all) || manifest.CorpusHash == "" || manifest.ProjectionHash == "" || manifest.Dimension < 1 || len(manifest.Indexes) != 4 {
		return report, ErrPublication
	}
	report.Manifest = &manifest
	if _, putErr := deps.Store.Put(ctx, memestore.Generation, manifest.Generation, manifest); putErr != nil {
		return report, ErrStorage
	}
	if activateErr := deps.Store.Activate(ctx, manifest.Generation); activateErr != nil {
		return report, ErrStorage
	}
	report.Active = true
	emit("active", "")
	return report, nil
}
