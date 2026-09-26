package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Live Bedrock discovery diagnostic / capture helper.
//
// This is NOT a validation test. It lets a developer with real AWS
// credentials capture actual control-plane and Price List responses to
// verify discovery output and re-check the pricing situation. It never
// runs in `go test ./...` unless explicitly enabled, so CI and unpaid
// checkouts never hit the network.
//
// Background: a live capture (Sep 2026, us-east-1) confirmed the AWS
// Price List API returns only stale, input-only rows for legacy Anthropic
// models (Claude 2.x / 3 Sonnet / 3 Haiku) and no output/cache prices for
// any current model, so zot does NOT enrich pricing from it — the catalog
// stays authoritative. This helper is retained to detect if/when AWS
// fixes that, and to confirm ListFoundationModels / ListInferenceProfiles
// shapes when they change.
//
// Enable and run:
//
//	ZOT_BEDROCK_CAPTURE=1 \
//	AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... [AWS_SESSION_TOKEN=...] \
//	AWS_REGION=us-east-1 \
//	go test ./packages/provider -run TestBedrockCaptureLive -v
//
// Or with a profile:
//
//	ZOT_BEDROCK_CAPTURE=1 AWS_PROFILE=my-sso AWS_REGION=us-east-1 \
//	go test ./packages/provider -run TestBedrockCaptureLive -v
//
// Output is written to files under $ZOT_BEDROCK_CAPTURE_DIR (default: a
// temp dir printed in the log). Secrets are never captured — only the
// public model/pricing JSON.
//
// Requires the caller's IAM principal to allow:
//   - bedrock:ListFoundationModels
//   - bedrock:ListInferenceProfiles
//   - pricing:GetProducts
func TestBedrockCaptureLive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ZOT_BEDROCK_CAPTURE")) == "" {
		t.Skip("set ZOT_BEDROCK_CAPTURE=1 (with AWS creds) to capture live Bedrock responses")
	}

	_, sigv4 := resolveBedrockAuth("")
	if sigv4 == nil {
		t.Fatal("no SigV4 credentials found; set AWS_ACCESS_KEY_ID+AWS_SECRET_ACCESS_KEY or AWS_PROFILE")
	}
	region := bedrockResolveRegion()
	t.Logf("capturing Bedrock responses for region=%s", region)

	outDir := strings.TrimSpace(os.Getenv("ZOT_BEDROCK_CAPTURE_DIR"))
	if outDir == "" {
		outDir = t.TempDir()
	}
	t.Logf("writing captures to %s", outDir)

	client := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. ListFoundationModels (control plane, GET).
	fmURL := "https://bedrock." + region + ".amazonaws.com/foundation-models"
	if body, err := bedrockSignedControlPlane(ctx, client, sigv4, region, http.MethodGet, fmURL, nil); err != nil {
		t.Errorf("ListFoundationModels failed: %v", err)
	} else {
		writeCapture(t, outDir, "foundation-models.json", body)
		// Also log the summarized model IDs so the capturer can eyeball them.
		logBedrockModelIDs(t, body)
	}

	// 2. ListInferenceProfiles (control plane, GET, paginated).
	ipURL := "https://bedrock." + region + ".amazonaws.com/inference-profiles?maxResults=1000"
	if body, err := bedrockSignedControlPlane(ctx, client, sigv4, region, http.MethodGet, ipURL, nil); err != nil {
		t.Errorf("ListInferenceProfiles failed: %v", err)
	} else {
		writeCapture(t, outDir, "inference-profiles.json", body)
	}

	// 3. GetProducts for AmazonBedrock (Price List API, POST).
	//    Capture the FIRST page raw so we can inspect real attribute
	//    names, inferenceType values, feature strings, and units. We
	//    deliberately do not paginate here — one page is enough to fix
	//    the fixtures, and it keeps the captured file small.
	captureBedrockPricingPage(ctx, t, client, sigv4, region, outDir)

	t.Logf("done. Inspect the files in %s and share them (they contain only public pricing/model data).", outDir)
}

// captureBedrockPricingPage issues a single GetProducts page filtered to
// the region and the Anthropic provider (the family we care about most)
// and writes the raw response. Anthropic is the case the aws-cli #9567
// report flagged as possibly missing output-token rows, so capturing it
// specifically lets us confirm or refute that.
func captureBedrockPricingPage(ctx context.Context, t *testing.T, client *http.Client, creds *bedrockSigV4Creds, region, outDir string) {
	const pricingRegion = "us-east-1"
	url := "https://api.pricing." + pricingRegion + ".amazonaws.com/"

	reqBody := map[string]any{
		"ServiceCode":   "AmazonBedrock",
		"FormatVersion": "aws_v1",
		"MaxResults":    100,
		"Filters": []map[string]string{
			{"Type": "TERM_MATCH", "Field": "regionCode", "Value": region},
			{"Type": "TERM_MATCH", "Field": "provider", "Value": "Anthropic"},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		t.Errorf("marshal pricing request: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(payload))
	if err != nil {
		t.Errorf("build pricing request: %v", err)
		return
	}
	req.Header.Set("content-type", "application/x-amz-json-1.1")
	req.Header.Set("x-amz-target", "AWSPriceListService.GetProducts")
	if err := signSigV4(req, payload, "pricing", pricingRegion, creds, time.Now().UTC()); err != nil {
		t.Errorf("sign pricing request: %v", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Errorf("pricing request failed: %v", err)
		return
	}
	defer resp.Body.Close()
	var page struct {
		PriceList []string `json:"PriceList"`
		NextToken string   `json:"NextToken"`
	}
	body := readAll(t, resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Errorf("pricing http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}
	writeCapture(t, outDir, "pricing-anthropic-raw.json", body)

	// Pretty-print each PriceList entry (they are JSON-encoded strings)
	// into a single readable file, and log a compact attribute summary.
	if err := json.Unmarshal(body, &page); err != nil {
		t.Errorf("parse pricing page: %v", err)
		return
	}
	t.Logf("pricing page returned %d PriceList entries (NextToken present: %v)", len(page.PriceList), page.NextToken != "")
	var pretty strings.Builder
	for _, raw := range page.PriceList {
		var obj any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		pretty.Write(b)
		pretty.WriteByte('\n')
		logBedrockPriceAttributes(t, raw)
	}
	writeCapture(t, outDir, "pricing-anthropic-pretty.json", []byte(pretty.String()))
}

func logBedrockModelIDs(t *testing.T, body []byte) {
	var fm struct {
		ModelSummaries []struct {
			ModelID          string   `json:"modelId"`
			OutputModalities []string `json:"outputModalities"`
		} `json:"modelSummaries"`
	}
	if err := json.Unmarshal(body, &fm); err != nil {
		return
	}
	t.Logf("ListFoundationModels: %d models", len(fm.ModelSummaries))
	for _, s := range fm.ModelSummaries {
		if strings.Contains(strings.ToLower(s.ModelID), "anthropic") {
			t.Logf("  modelId=%s outputModalities=%v", s.ModelID, s.OutputModalities)
		}
	}
}

func logBedrockPriceAttributes(t *testing.T, raw string) {
	var entry struct {
		Product struct {
			Attributes map[string]string `json:"attributes"`
		} `json:"product"`
	}
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return
	}
	a := entry.Product.Attributes
	t.Logf("  price: model=%q inferenceType=%q feature=%q usagetype=%q",
		a["model"], a["inferenceType"], a["feature"], a["usagetype"])
}

func writeCapture(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	path := dir + string(os.PathSeparator) + name
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Errorf("write %s: %v", path, err)
		return
	}
	t.Logf("wrote %s (%d bytes)", path, len(body))
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
		if len(buf) > 32<<20 {
			break
		}
	}
	return buf
}
