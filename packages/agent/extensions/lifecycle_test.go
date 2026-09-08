package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

func lifecyclePeer(t *testing.T, m *Manager, names ...string) (<-chan []byte, *Extension) {
	t.Helper()
	reader, writer := io.Pipe()
	out := make(chan []byte, 512)
	done := make(chan struct{})
	pipe := newOrderedPipe(writer)
	ext := &Extension{stdin: pipe, eventSubs: map[string]struct{}{}, interceptSubs: map[string]struct{}{}, pendingIntercept: map[string]chan extproto.EventInterceptResponseFromExt{}}
	for _, name := range names {
		ext.eventSubs[name] = struct{}{}
	}
	m.ext["test"] = ext
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			out <- append([]byte(nil), scanner.Bytes()...)
		}
		close(out)
	}()
	t.Cleanup(func() { pipe.Close(); reader.Close(); <-done })
	return out, ext
}

func nextLifecycleFrame(t *testing.T, frames <-chan []byte) []byte {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("peer closed")
		}
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
		return nil
	}
}

func TestLifecycleOrderAndShutdownDrain(t *testing.T) {
	m := New(t.TempDir(), "/work", "test", "", "", nil)
	frames, _ := lifecyclePeer(t, m, "session_start", "session_end", "tool_call", "tool_result")
	m.StartSession("one", "/work", "")
	m.StartSession("one", "/work", "") // idempotent
	for n := 0; n < 50; n++ {
		m.EmitEvent(extproto.EventFromHost{Event: "tool_call", Step: n})
		m.EmitEvent(extproto.EventFromHost{Event: "tool_result", Step: n})
	}
	m.StartSession("two", "/other", "cwd_change")
	m.EndSession("user_exit")
	m.EndSession("shutdown")
	m.Stop(time.Second)
	m.StartSession("late", "/late", "")
	m.EmitEvent(extproto.EventFromHost{Event: "tool_result"})
	if id, _ := m.SessionContext(); id != "two" {
		t.Fatal("late callback reopened a stopped session")
	}
	var got []extproto.EventFromHost
	for {
		raw := nextLifecycleFrame(t, frames)
		var e extproto.EventFromHost
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		if e.Type == "shutdown" {
			break
		}
		if e.Sequence != uint64(len(got)+1) {
			t.Fatalf("out of order: %+v", e)
		}
		got = append(got, e)
	}
	if len(got) != 104 {
		t.Fatalf("got %d events", len(got))
	}
	for n := 0; n < 50; n++ {
		a, b := got[1+2*n], got[2+2*n]
		if a.Event != "tool_call" || b.Event != "tool_result" || a.Step != n || b.Step != n {
			t.Fatalf("wrong order at %d", n)
		}
	}
	if got[101].Event != "session_end" || got[101].SessionID != "one" || got[101].Reason != "cwd_change" || got[101].CWD != "/work" {
		t.Fatalf("old boundary: %+v", got[101])
	}
	if got[102].Event != "session_start" || got[102].SessionID != "two" || got[102].CWD != "/other" {
		t.Fatalf("new boundary: %+v", got[102])
	}
	if got[103].Event != "session_end" || got[103].Reason != "user_exit" {
		t.Fatalf("end: %+v", got[103])
	}
}

func TestNotificationPrecedesInterceptOnWire(t *testing.T) {
	m := New(t.TempDir(), "/work", "test", "", "", nil)
	frames, ext := lifecyclePeer(t, m, "tool_call")
	ext.interceptSubs["tool_call"] = struct{}{}
	m.EmitEvent(extproto.EventFromHost{Event: "tool_call", ToolID: "call"})
	result := make(chan InterceptResult, 1)
	go func() { result <- m.InterceptToolCall(context.Background(), "call", "bash", json.RawMessage(`{}`)) }()
	var event extproto.EventFromHost
	json.Unmarshal(nextLifecycleFrame(t, frames), &event)
	if event.Type != "event" || event.Event != "tool_call" {
		t.Fatalf("first frame: %+v", event)
	}
	var intercept extproto.EventInterceptFromHost
	json.Unmarshal(nextLifecycleFrame(t, frames), &intercept)
	if intercept.Type != "event_intercept" {
		t.Fatalf("second frame: %+v", intercept)
	}
	ext.mu.Lock()
	reply := ext.pendingIntercept[intercept.ID]
	delete(ext.pendingIntercept, intercept.ID)
	ext.mu.Unlock()
	reply <- extproto.EventInterceptResponseFromExt{Block: true}
	select {
	case r := <-result:
		if !r.Block {
			t.Fatal("block lost")
		}
	case <-time.After(time.Second):
		t.Fatal("interceptor did not finish")
	}
}

func TestSlowConsumerOverflowClosesPipe(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	pipe := newOrderedPipe(writer)
	defer pipe.Close()
	var err error
	for n := 0; n < outboundFrames+2; n++ {
		err = pipe.enqueue([]byte("{}\n"), nil)
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("unbounded queue")
	}
	select {
	case <-pipe.done:
	default:
		t.Fatal("overflow did not disconnect")
	}
	if _, err := pipe.Write([]byte("{}\n")); err == nil {
		t.Fatal("write after disconnect succeeded")
	}
}

func TestLifecycleSubscriptionFiltering(t *testing.T) {
	m := New(t.TempDir(), "/work", "test", "", "", nil)
	frames, ext := lifecyclePeer(t, m, "post_compact", "permission_decision", "subagent_start", "subagent_stop", "user_prompt_submit")
	names := []string{"post_compact", "permission_decision", "subagent_start", "subagent_stop", "user_prompt_submit"}
	m.EmitEvent(extproto.EventFromHost{Event: "pre_compact"})
	for _, name := range names {
		m.EmitEvent(extproto.EventFromHost{Event: name})
	}
	if _, err := ext.stdin.Write([]byte("{\"type\":\"marker\"}\n")); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range names {
		var e extproto.EventFromHost
		json.Unmarshal(nextLifecycleFrame(t, frames), &e)
		got = append(got, e.Event)
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("got %v", got)
	}
}

func TestMalformedArgumentsStillEmitResult(t *testing.T) {
	m := New(t.TempDir(), "/work", "test", "", "", nil)
	frames, _ := lifecyclePeer(t, m, "tool_result")
	m.EmitEvent(extproto.EventFromHost{Event: "tool_result", ToolID: "bad-call", ToolArgs: json.RawMessage(`{invalid`), Status: "failed"})
	var e extproto.EventFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &e); err != nil {
		t.Fatal(err)
	}
	if e.ToolID != "bad-call" || e.Status != "failed" || len(e.ToolArgs) != 0 || e.ToolArgsRaw != `{invalid` {
		t.Fatalf("result: %+v", e)
	}
}

func TestShutdownBoundsBlockedWriter(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	pipe := newOrderedPipe(writer)
	defer pipe.Close()
	if err := pipe.enqueue([]byte("{}\n"), nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { stopExtensions([]*Extension{{stdin: pipe}}, time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on a slow consumer")
	}
}

func TestExtensionToolTerminalStatus(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "timed_out"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			m := New(t.TempDir(), "/work", "test", "", "", nil)
			_, ext := lifecyclePeer(t, m)
			ext.pendingTool = map[string]chan extproto.ToolResultFromExt{}
			m.toolIndex["probe"] = ext
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := time.Nanosecond
			if cancelled {
				cancel()
				timeout = time.Hour
			}
			tool := &extensionTool{name: "probe", extension: "test", manager: m, timeout: timeout}
			result, err := tool.Execute(ctx, json.RawMessage(`{}`), nil)
			if err != nil || !result.IsError || result.Status != name {
				t.Fatalf("result: %+v, error: %v", result, err)
			}
			ext.mu.Lock()
			pending := len(ext.pendingTool)
			ext.mu.Unlock()
			if pending != 0 {
				t.Fatal("terminal call left a pending reply")
			}
		})
	}
}
