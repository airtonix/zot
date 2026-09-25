package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBedrockEmitsText(t *testing.T) {
	if !bedrockEmitsText(nil) {
		t.Error("nil modalities should default to text")
	}
	if !bedrockEmitsText([]string{"TEXT"}) {
		t.Error("TEXT should emit text")
	}
	if bedrockEmitsText([]string{"IMAGE"}) {
		t.Error("IMAGE-only should not emit text")
	}
	if !bedrockEmitsText([]string{"IMAGE", "TEXT"}) {
		t.Error("mixed with TEXT should emit text")
	}
}

func TestResolveBedrockAuthBearerVsSigV4(t *testing.T) {
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")

	// Explicit bearer via apiKey.
	if b, s := resolveBedrockAuth("tok-123"); b != "tok-123" || s != nil {
		t.Errorf("explicit bearer: got (%q,%v)", b, s)
	}

	// Sentinel <aws> is not a bearer; falls through to SigV4 env.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKID")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRET")
	b, s := resolveBedrockAuth("<aws>")
	if b != "" || s == nil {
		t.Fatalf("sentinel should resolve SigV4, got (%q,%v)", b, s)
	}
	if s.accessKeyID != "AKID" || s.secretAccessKey != "SECRET" {
		t.Errorf("sigv4 creds = %+v", s)
	}
}

// TestBedrockListModelIDs parses control-plane responses shaped exactly
// like the real ones captured from a live account (see the doc note in
// discover_bedrock.go): ListFoundationModels returns modelSummaries with
// modelId + outputModalities; ListInferenceProfiles returns
// inferenceProfileSummaries with inferenceProfileId and paginates via
// nextToken. Embedding/image-only models are filtered out; profile IDs
// are included; duplicates are de-duplicated.
func TestBedrockListModelIDs(t *testing.T) {
	page1 := `{"inferenceProfileSummaries":[{"inferenceProfileId":"us.anthropic.claude-opus-5-5"}],"nextToken":"NEXT"}`
	page2 := `{"inferenceProfileSummaries":[{"inferenceProfileId":"eu.anthropic.claude-sonnet-5"}]}`
	foundation := `{"modelSummaries":[
		{"modelId":"anthropic.claude-opus-5-5","outputModalities":["TEXT"]},
		{"modelId":"amazon.titan-embed-text-v2","outputModalities":["EMBEDDING"]},
		{"modelId":"stability.sd3-large","outputModalities":["IMAGE"]},
		{"modelId":"anthropic.claude-opus-5-5","outputModalities":["TEXT"]}
	]}`

	var ipHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/foundation-models"):
			w.Write([]byte(foundation))
		case strings.Contains(r.URL.Path, "/inference-profiles"):
			ipHits++
			if r.URL.Query().Get("nextToken") == "NEXT" {
				w.Write([]byte(page2))
			} else {
				w.Write([]byte(page1))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	creds := &bedrockSigV4Creds{accessKeyID: "AKID", secretAccessKey: "SECRET"}
	ids, err := bedrockListModelIDs(context.Background(), srv.Client(), creds, "us-east-1", srv.URL)
	if err != nil {
		t.Fatalf("bedrockListModelIDs: %v", err)
	}

	want := []string{
		"anthropic.claude-opus-5-5",    // text foundation model, de-duplicated
		"us.anthropic.claude-opus-5-5", // profile page 1
		"eu.anthropic.claude-sonnet-5", // profile page 2 (pagination followed)
	}
	got := map[string]int{}
	for _, id := range ids {
		got[id]++
	}
	for _, w := range want {
		if got[w] != 1 {
			t.Errorf("expected exactly one %q, got %d (all=%v)", w, got[w], ids)
		}
	}
	// Embedding and image-only models must be filtered out.
	for _, bad := range []string{"amazon.titan-embed-text-v2", "stability.sd3-large"} {
		if got[bad] != 0 {
			t.Errorf("non-text model %q should be filtered out", bad)
		}
	}
	if ipHits != 2 {
		t.Errorf("expected 2 inference-profile page requests (pagination), got %d", ipHits)
	}
}

// TestBedrockControlPlaneHTTPError confirms non-2xx responses surface as
// errors with the status and body, so discovery fails loudly (and the
// caller then falls back to the catalog) rather than silently parsing junk.
func TestBedrockControlPlaneHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"not authorized"}`))
	}))
	defer srv.Close()

	creds := &bedrockSigV4Creds{accessKeyID: "AKID", secretAccessKey: "SECRET"}
	_, err := bedrockSignedControlPlane(context.Background(), srv.Client(), creds, "us-east-1", http.MethodGet, srv.URL+"/foundation-models", nil)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("error should carry status and body: %v", err)
	}
}
