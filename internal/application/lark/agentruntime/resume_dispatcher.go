package agentruntime

import (
	"context"
	"fmt"
)

type runResumer interface {
	ResumeRun(context.Context, ResumeEvent) (*AgentRun, error)
}

type resumeEnqueuer interface {
	EnqueueResumeEvent(context.Context, ResumeEvent) error
}

type ResumeDispatcher struct {
	resumer  runResumer
	enqueuer resumeEnqueuer
}

func NewResumeDispatcher(resumer runResumer, enqueuer resumeEnqueuer) *ResumeDispatcher {
	return &ResumeDispatcher{
		resumer:  resumer,
		enqueuer: enqueuer,
	}
}

func (d *ResumeDispatcher) Dispatch(ctx context.Context, event ResumeEvent) (*AgentRun, error) {
	if d == nil {
		return nil, fmt.Errorf("resume dispatcher is nil")
	}
	if d.resumer == nil {
		return nil, fmt.Errorf("resume dispatcher resumer is nil")
	}
	if d.enqueuer == nil {
		return nil, fmt.Errorf("resume dispatcher enqueuer is nil")
	}

	run, err := d.resumer.ResumeRun(ctx, event)
	if err != nil {
		return nil, err
	}
	if err := d.enqueuer.EnqueueResumeEvent(ctx, event); err != nil {
		return nil, err
	}
	return run, nil
}
