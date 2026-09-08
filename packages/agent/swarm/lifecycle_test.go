package swarm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func nextSwarmLifecycle(t *testing.T, events <-chan LifecycleEvent) LifecycleEvent {
	t.Helper()
	select {
	case e := <-events:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("missing lifecycle event")
		return LifecycleEvent{}
	}
}

func TestSubagentLifecycle(t *testing.T) {
	for _, tc := range []struct {
		status string
		err    error
	}{
		{"completed", nil}, {"failed", errors.New("failed")}, {"cancelled", context.Canceled}, {"timed_out", context.DeadlineExceeded},
	} {
		t.Run(tc.status, func(t *testing.T) {
			root := t.TempDir()
			events := make(chan LifecycleEvent, 2)
			f := New(Config{Root: root, RepoRoot: root, OnLifecycle: func(e LifecycleEvent) { events <- e }, NewRunner: func(*Agent) Runner {
				return RunnerFunc(func(context.Context, Sink) error { return tc.err })
			}})
			f.SetActiveSession("parent")
			a, err := f.Spawn(context.Background(), "review")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { a.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("runner did not finish")
			}
			first, last := nextSwarmLifecycle(t, events), nextSwarmLifecycle(t, events)
			if first.Event != "subagent_start" || last.Event != "subagent_stop" || last.Status != tc.status || first.AgentRunID == "" || first.AgentRunID != last.AgentRunID {
				t.Fatalf("events: %+v %+v", first, last)
			}
			for _, e := range []LifecycleEvent{first, last} {
				if e.SessionID != "parent" || e.AgentID != a.ID || e.CWD != root {
					t.Fatalf("identity: %+v", e)
				}
			}
			if err := f.Stop(a.ID); err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-events:
				t.Fatalf("duplicate event: %+v", e)
			default:
			}
		})
	}
}

func TestShutdownWaitsForSubagentStop(t *testing.T) {
	root := t.TempDir()
	events := make(chan LifecycleEvent, 2)
	started := make(chan struct{})
	f := New(Config{Root: root, RepoRoot: root, OnLifecycle: func(e LifecycleEvent) { events <- e }, NewRunner: func(*Agent) Runner {
		return RunnerFunc(func(ctx context.Context, _ Sink) error { close(started); <-ctx.Done(); return ctx.Err() })
	}})
	if _, err := f.Spawn(context.Background(), "wait"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	f.Shutdown(ctx)
	if ctx.Err() != nil {
		t.Fatal("shutdown did not finish")
	}
	if nextSwarmLifecycle(t, events).Event != "subagent_start" {
		t.Fatal("missing start")
	}
	if e := nextSwarmLifecycle(t, events); e.Event != "subagent_stop" || e.Status != "cancelled" {
		t.Fatalf("stop: %+v", e)
	}
}

func TestResumedSubagentHasNewLifecycleRun(t *testing.T) {
	root := t.TempDir()
	events := make(chan LifecycleEvent, 4)
	f := New(Config{Root: root, RepoRoot: root, OnLifecycle: func(e LifecycleEvent) { events <- e }, NewRunner: func(*Agent) Runner {
		return RunnerFunc(func(context.Context, Sink) error { return nil })
	}})
	f.SetActiveSession("original")
	a, err := f.Spawn(context.Background(), "review")
	if err != nil {
		t.Fatal(err)
	}
	first := nextSwarmLifecycle(t, events)
	nextSwarmLifecycle(t, events)
	a.Wait()
	f.SetActiveSession("other")
	resumed, err := f.Resume(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	start, stop := nextSwarmLifecycle(t, events), nextSwarmLifecycle(t, events)
	resumed.Wait()
	if start.Event != "subagent_start" || stop.Event != "subagent_stop" || start.AgentRunID == first.AgentRunID || start.AgentRunID != stop.AgentRunID || start.SessionID != "original" || stop.SessionID != "original" {
		t.Fatalf("resume: %+v %+v", start, stop)
	}
}
