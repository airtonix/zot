package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patriceckhart/zot/packages/core"
)

func isolateSessionEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ZOT_SESSION_ID", "ZOT_SESSION_FILE", "ZOT_PROVIDER", "ZOT_MODEL", "ZOT_REASONING_LEVEL"} {
		t.Setenv(name, "inherited")
	}
}

func assertSessionEnvironment(t *testing.T, r Resolved, sess *core.Session) {
	t.Helper()
	want := map[string]string{
		"ZOT_SESSION_ID": "", "ZOT_SESSION_FILE": "",
		"ZOT_PROVIDER": r.Provider, "ZOT_MODEL": r.Model, "ZOT_REASONING_LEVEL": r.Reasoning,
	}
	if sess != nil {
		want["ZOT_SESSION_ID"] = sess.ID
		want["ZOT_SESSION_FILE"] = sess.Path
	}
	for name, value := range want {
		got, present := os.LookupEnv(name)
		if got != value || present != (value != "") {
			t.Errorf("%s = %q (present=%v), want %q (present=%v)", name, got, present, value, value != "")
		}
	}
}

func TestOpenSessionWithoutPersistencePublishesModelEnvironment(t *testing.T) {
	isolateSessionEnvironment(t)
	r := Resolved{Provider: "test-provider", Model: "test-model", Reasoning: "high"}
	sess, err := openOrCreateSession(Args{NoSess: true}, r, nil, "test")
	if err != nil || sess != nil {
		t.Fatalf("openOrCreateSession = %v, %v; want nil, nil", sess, err)
	}
	assertSessionEnvironment(t, r, nil)
}

func TestSessionEnvironmentReplacesSessionAndClearsReasoning(t *testing.T) {
	isolateSessionEnvironment(t)
	r := Resolved{Provider: "old-provider", Model: "old-model", Reasoning: "high"}
	oldSession := &core.Session{ID: "old", Path: filepath.Join(t.TempDir(), "old.jsonl")}
	setZotSessionEnvironment(r, oldSession)
	assertSessionEnvironment(t, r, oldSession)

	// A directory change replaces the session and may rebuild a different model.
	r.Provider, r.Model, r.Reasoning = "new-provider", "new-model", "medium"
	newSession := &core.Session{ID: "new", Path: filepath.Join(t.TempDir(), "new.jsonl")}
	setZotSessionEnvironment(r, newSession)
	assertSessionEnvironment(t, r, newSession)

	r.Reasoning = ""
	setZotSessionEnvironment(r, newSession)
	assertSessionEnvironment(t, r, newSession)

	setZotSessionEnvironment(r, nil)
	assertSessionEnvironment(t, r, nil)
}
