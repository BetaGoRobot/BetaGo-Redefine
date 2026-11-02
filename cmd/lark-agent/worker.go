package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/worker"
)

func processing(ctx context.Context, c config.Config, listen string) error {
	parts, err := initialize(ctx, c)
	if err != nil {
		return err
	}
	defer parts.close()
	runID, err := owner()
	if err != nil {
		return err
	}
	runners := make([]func(context.Context) error, 0, c.Worker.Concurrency+1)
	for i := range c.Worker.Concurrency {
		w, err := worker.New(worker.Config{Store: parts.store, Index: parts.index, Memory: parts.memory, Media: parts.media, Embedder: parts.embed, Owner: fmt.Sprintf("%s-%d", runID, i), Lease: time.Duration(c.Worker.LeaseSeconds) * time.Second, Poll: time.Duration(c.Worker.PollMilliseconds) * time.Millisecond})
		if err != nil {
			return errors.New("processing worker initialization failed")
		}
		runners = append(runners, w.Run)
	}
	runners = append(runners, func(ctx context.Context) error { return serve(ctx, listen, readyHandler(parts.db, nil)) })
	if err := runServices(ctx, runners...); err != nil {
		return errors.New("processing service stopped")
	}
	return nil
}
