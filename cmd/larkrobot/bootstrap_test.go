package main

import (
	"context"
	"errors"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

type fakeWorkerHandle struct {
	available bool
	started   bool
	stopped   bool
}

func (f *fakeWorkerHandle) Start() {
	f.started = true
}

func (f *fakeWorkerHandle) Stop() {
	f.stopped = true
}

func (f *fakeWorkerHandle) Stats() map[string]any {
	return nil
}

func (f *fakeWorkerHandle) Available() bool {
	return f != nil && f.available
}

func TestStartAgentRuntimeResumeWorkerStartsAvailableWorkerWithoutGlobalConfigGate(t *testing.T) {
	originalBuilder := buildAgentRuntimeResumeWorker
	originalResumeWorker := resumeWorker
	originalPendingScopeSweeper := pendingScopeSweeper
	defer func() {
		buildAgentRuntimeResumeWorker = originalBuilder
		resumeWorker = originalResumeWorker
		pendingScopeSweeper = originalPendingScopeSweeper
	}()

	fake := &fakeWorkerHandle{available: true}
	buildAgentRuntimeResumeWorker = func(context.Context) workerHandle {
		return fake
	}
	resumeWorker = nil

	if err := startAgentRuntimeResumeWorker(context.Background()); err != nil {
		t.Fatalf("startAgentRuntimeResumeWorker() error = %v", err)
	}
	if !fake.started {
		t.Fatal("expected resume worker to be started")
	}
	if resumeWorker != fake {
		t.Fatal("expected started worker to be stored globally")
	}
}

func TestStartAgentRuntimeResumeWorkerReturnsDisabledWhenUnavailable(t *testing.T) {
	originalBuilder := buildAgentRuntimeResumeWorker
	originalResumeWorker := resumeWorker
	originalPendingScopeSweeper := pendingScopeSweeper
	defer func() {
		buildAgentRuntimeResumeWorker = originalBuilder
		resumeWorker = originalResumeWorker
		pendingScopeSweeper = originalPendingScopeSweeper
	}()

	buildAgentRuntimeResumeWorker = func(context.Context) workerHandle {
		return &fakeWorkerHandle{available: false}
	}
	resumeWorker = nil

	err := startAgentRuntimeResumeWorker(context.Background())
	if !errors.Is(err, appruntime.ErrDisabled) {
		t.Fatalf("startAgentRuntimeResumeWorker() error = %v, want %v", err, appruntime.ErrDisabled)
	}
}

func TestStartPendingScopeSweeperStartsAvailableWorker(t *testing.T) {
	originalBuilder := buildPendingScopeSweeper
	originalPendingScopeSweeper := pendingScopeSweeper
	defer func() {
		buildPendingScopeSweeper = originalBuilder
		pendingScopeSweeper = originalPendingScopeSweeper
	}()

	fake := &fakeWorkerHandle{available: true}
	buildPendingScopeSweeper = func(context.Context) workerHandle {
		return fake
	}
	pendingScopeSweeper = nil

	if err := startPendingScopeSweeper(context.Background()); err != nil {
		t.Fatalf("startPendingScopeSweeper() error = %v", err)
	}
	if !fake.started {
		t.Fatal("expected pending scope sweeper to be started")
	}
	if pendingScopeSweeper != fake {
		t.Fatal("expected started pending scope sweeper to be stored globally")
	}
}

func TestStartPendingScopeSweeperReturnsDisabledWhenUnavailable(t *testing.T) {
	originalBuilder := buildPendingScopeSweeper
	originalPendingScopeSweeper := pendingScopeSweeper
	defer func() {
		buildPendingScopeSweeper = originalBuilder
		pendingScopeSweeper = originalPendingScopeSweeper
	}()

	buildPendingScopeSweeper = func(context.Context) workerHandle {
		return &fakeWorkerHandle{available: false}
	}
	pendingScopeSweeper = nil

	err := startPendingScopeSweeper(context.Background())
	if !errors.Is(err, appruntime.ErrDisabled) {
		t.Fatalf("startPendingScopeSweeper() error = %v, want %v", err, appruntime.ErrDisabled)
	}
}

func TestAddInfrastructureModulesRegistersAKShareAPIModule(t *testing.T) {
	app := appruntime.NewApp()

	addInfrastructureModules(app, &infraConfig.BaseConfig{})

	snapshot := app.Registry().Snapshot()
	if hasComponent(snapshot.Components, "aktool") {
		t.Fatalf("unexpected aktool module in registry: %+v", snapshot.Components)
	}
	if !hasComponent(snapshot.Components, "akshareapi") {
		t.Fatalf("expected akshareapi module in registry: %+v", snapshot.Components)
	}
}

func hasComponent(items []appruntime.ComponentStatus, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
