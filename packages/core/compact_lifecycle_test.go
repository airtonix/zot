package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/patriceckhart/zot/packages/provider"
)

type compactLifecycleClient struct{ err error }

func (*compactLifecycleClient) Name() string { return "test" }
func (c *compactLifecycleClient) Stream(context.Context, provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event, 2)
	ch <- provider.EventTextDelta{Delta: "summary"}
	ch <- provider.EventDone{Err: c.err}
	close(ch)
	return ch, nil
}

func TestCompactionTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		status string
		err    error
	}{
		{"completed", nil}, {"failed", errors.New("failure")}, {"cancelled", context.Canceled}, {"timed_out", context.DeadlineExceeded},
	} {
		t.Run(tc.status, func(t *testing.T) {
			a := NewAgent(&compactLifecycleClient{err: tc.err}, "test", "", nil)
			before := []provider.Message{
				{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "first message"}}},
				{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "second message"}}},
			}
			a.SetMessages(before)
			var events []EvCompact
			a.OnEvent = func(e AgentEvent) { events = append(events, e.(EvCompact)) }
			_, err := a.Compact(context.Background(), 0, nil)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error = %v", err)
			}
			if len(events) != 2 || events[0].Phase != "pre" || events[1].Phase != "post" || events[1].Status != tc.status || events[0].ID == "" || events[0].ID != events[1].ID {
				t.Fatalf("events: %+v", events)
			}
			if events[0].MessageCount != 2 || events[0].TokenEstimate <= 0 {
				t.Fatalf("pre counts: %+v", events[0])
			}
			if tc.err == nil {
				if events[1].MessageCount != 1 || events[1].TokenEstimate <= 0 {
					t.Fatalf("post counts: %+v", events[1])
				}
			} else if !reflect.DeepEqual(a.Messages(), before) {
				t.Fatal("failed compaction changed transcript")
			}
		})
	}
}
