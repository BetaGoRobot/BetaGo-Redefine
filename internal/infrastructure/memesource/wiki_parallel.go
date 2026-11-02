package memesource

import (
	"context"
	"encoding/json"
	"sync"
)

type catalogDetailJob struct {
	slug string
	raw  json.RawMessage
}

type catalogDetailResult struct {
	index  int
	source Source
	err    error
}

// fetchPage overlaps bounded HTTP reads, but emits source snapshots and progress
// in directory order. The sink can feed encoders before the page has finished.
func (c *WikiClient) fetchPage(parent context.Context, details []catalogDetailJob, parallel int, catalog *Catalog, sink func(Source) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	jobs := make(chan int, len(details))
	for i := range details {
		jobs <- i
	}
	close(jobs)
	results := make(chan catalogDetailResult, parallel)
	var workers sync.WaitGroup
	for worker := 0; worker < parallel; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				source, err := c.detail(ctx, details[i].slug, details[i].raw)
				select {
				case results <- catalogDetailResult{index: i, source: source, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()
	defer func() { cancel(); workers.Wait() }()
	ordered := make([]catalogDetailResult, len(details))
	ready := make([]bool, len(details))
	next := 0
	for result := range results {
		ordered[result.index] = result
		ready[result.index] = true
		for next < len(details) && ready[next] {
			item := ordered[next]
			sourceID := "wiki:" + details[next].slug
			if item.err != nil {
				catalog.Errors = append(catalog.Errors, SourceError{SourceID: sourceID, Stage: "detail", Error: item.err.Error()})
			} else {
				catalog.Sources = append(catalog.Sources, item.source)
			}
			c.progress("source", *catalog, sourceID)
			if item.err == nil && sink != nil {
				if err := sink(item.source); err != nil {
					return err
				}
			}
			next++
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if next != len(details) {
		return ErrWikiTransport
	}
	return nil
}
