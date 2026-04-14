package engram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdkengram "github.com/bubustack/bubu-sdk-go/engram"
	"github.com/bubustack/http-request-engram/pkg/config"
)

func TestProcessStreamMessageMirrorsPayloadToBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	engine := New()
	if err := engine.Init(context.Background(), config.Config{}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	msg, ok, err := engine.processStreamMessage(
		context.Background(),
		slog.Default(),
		sdkengram.InboundMessage{
			StreamMessage: sdkengram.StreamMessage{
				Inputs: []byte(`{"url":"` + server.URL + `"}`),
			},
		},
	)
	if err != nil {
		t.Fatalf("processStreamMessage returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected processStreamMessage to emit an output")
	}
	if msg.Binary == nil {
		t.Fatal("expected binary payload")
	}
	if string(msg.Payload) != string(msg.Binary.Payload) {
		t.Fatalf("expected mirrored payload, payload=%q binary=%q", string(msg.Payload), string(msg.Binary.Payload))
	}
	var output map[string]any
	if err := json.Unmarshal(msg.Payload, &output); err != nil {
		t.Fatalf("failed to decode output payload: %v", err)
	}
	if output == nil {
		t.Fatal("expected structured output payload")
	}
}

func TestDecodeStreamInputsPrefersPayloadOverBinary(t *testing.T) {
	inputs, err := decodeStreamInputs(
		sdkengram.NewInboundMessage(sdkengram.StreamMessage{
			Payload: []byte(`{"url":"https://payload.example"}`),
			Binary: &sdkengram.BinaryFrame{
				Payload:  []byte(`{"url":"https://binary.example"}`),
				MimeType: "application/json",
			},
		}),
	)
	if err != nil {
		t.Fatalf("decodeStreamInputs returned error: %v", err)
	}
	if inputs["url"] != "https://payload.example" {
		t.Fatalf("expected payload URL to win, got %#v", inputs["url"])
	}
}

func TestAuthAndProxyHandleNilSecrets(t *testing.T) {
	engine := &HTTPRequestEngram{
		config: config.Config{
			Proxy: "http://proxy.example:8080",
			Auth:  &config.AuthConfig{Type: "bearer"},
		},
		// secrets intentionally left nil
	}

	client, err := engine.buildClient()
	if err != nil {
		t.Fatalf("buildClient returned error: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	if err := engine.applyAuth(req); err != nil {
		t.Fatalf("applyAuth returned error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("expected Authorization to stay empty without secrets, got %q", got)
	}
}

func TestFormatResponseBodyFileReturnsStructuredBlob(t *testing.T) {
	const bodyText = "hello file"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte(bodyText))
	}))
	defer server.Close()

	engine := New()
	if err := engine.Init(context.Background(), config.Config{
		Response: &config.ResponseConfig{
			Format:          "file",
			OutputFieldName: "body",
		},
	}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	output, err := engine.doRequest(context.Background(), slog.Default(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("doRequest returned error: %v", err)
	}
	got, ok := output["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected file body map, got %T", output["body"])
	}
	if got["encoding"] != "base64" {
		t.Fatalf("expected base64 encoding, got %#v", got["encoding"])
	}
	if got["data"] != base64.StdEncoding.EncodeToString([]byte(bodyText)) {
		t.Fatalf("unexpected file data value: %#v", got["data"])
	}
	if got["contentType"] != "application/pdf" {
		t.Fatalf("unexpected contentType: %#v", got["contentType"])
	}
	if got["sizeBytes"] != len(bodyText) {
		t.Fatalf("unexpected sizeBytes: %#v", got["sizeBytes"])
	}
}

func TestCloneStreamMessageClonesPayload(t *testing.T) {
	msg := sdkengram.NewInboundMessage(sdkengram.StreamMessage{
		Payload: []byte(`{"url":"https://example.com"}`),
	})

	clone := cloneStreamMessage(msg)
	if string(clone.Payload) != string(msg.Payload) {
		t.Fatalf("expected payload to be cloned, got %q want %q", string(clone.Payload), string(msg.Payload))
	}
	clone.Payload[0] = 'X'
	if string(msg.Payload) == string(clone.Payload) {
		t.Fatal("expected cloned payload to be independent from original payload")
	}
}

func TestLaunchStreamWorkerMarksDoneOnWorkerError(t *testing.T) {
	engine := New()
	if err := engine.Init(context.Background(), config.Config{}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	doneCh := make(chan struct{}, 1)
	msg := sdkengram.BindProcessingReceipt(
		sdkengram.NewInboundMessage(sdkengram.StreamMessage{
			Payload: []byte(`{"url":`), // invalid JSON to trigger decode error
		}),
		func() {
			doneCh <- struct{}{}
		},
	)

	var wg sync.WaitGroup
	out := make(chan sdkengram.StreamMessage)
	errCh := make(chan error, 1)
	engine.launchStreamWorker(context.Background(), slog.Default(), &wg, msg, out, errCh)
	wg.Wait()

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected message.Done to be called on worker error")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil stream worker error")
		}
	default:
		t.Fatal("expected worker error to be reported")
	}
}

func TestLaunchStreamWorkerMarksDoneOnContextCancelWhileSending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	engine := New()
	if err := engine.Init(context.Background(), config.Config{}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	doneCh := make(chan struct{}, 1)
	msg := sdkengram.BindProcessingReceipt(
		sdkengram.NewInboundMessage(sdkengram.StreamMessage{
			Inputs: []byte(`{"url":"` + server.URL + `"}`),
		}),
		func() {
			doneCh <- struct{}{}
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	out := make(chan sdkengram.StreamMessage) // unbuffered and intentionally unread to block send
	errCh := make(chan error, 1)
	engine.launchStreamWorker(ctx, slog.Default(), &wg, msg, out, errCh)
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected message.Done to be called on context cancel path")
	}
}

func TestPrepareNextURLRequestRejectsCrossOriginURL(t *testing.T) {
	engine := New()
	if err := engine.Init(context.Background(), config.Config{
		Pagination: &config.PaginationConfig{
			Mode:        "nextUrl",
			NextURLPath: "$.next",
		},
	}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	current := map[string]any{"url": "https://api.example.com/v1/resources?page=1"}
	hasNext, _, err := engine.prepareNextURLRequest(current, map[string]any{
		"next": "https://evil.example.com/steal?page=2",
	})
	if err == nil {
		t.Fatal("expected origin change error")
	}
	if !strings.Contains(err.Error(), "origin change") {
		t.Fatalf("expected origin change error message, got %v", err)
	}
	if hasNext {
		t.Fatal("expected hasNext=false on rejected origin change")
	}
}

func TestPrepareNextURLRequestResolvesRelativeURLOnSameOrigin(t *testing.T) {
	engine := New()
	if err := engine.Init(context.Background(), config.Config{
		Pagination: &config.PaginationConfig{
			Mode:        "nextUrl",
			NextURLPath: "$.next",
		},
	}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	current := map[string]any{
		"url":    "https://api.example.com/v1/resources?page=1",
		"params": map[string]any{"page": "1"},
	}
	hasNext, _, err := engine.prepareNextURLRequest(current, map[string]any{
		"next": "/v1/resources?page=2",
	})
	if err != nil {
		t.Fatalf("expected no error for same-origin relative next URL, got %v", err)
	}
	if !hasNext {
		t.Fatal("expected hasNext=true for valid next URL")
	}
	if got := current["url"]; got != "https://api.example.com/v1/resources?page=2" {
		t.Fatalf("unexpected resolved URL: %#v", got)
	}
	if _, ok := current["params"]; ok {
		t.Fatal("expected params to be removed when next URL is applied")
	}
}

func TestBuildHTTPRequestRejectsUnsupportedBodyType(t *testing.T) {
	engine := New()
	if err := engine.Init(context.Background(), config.Config{}, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	_, err := engine.buildHTTPRequest(context.Background(), map[string]any{
		"url":  "https://api.example.com/v1/resources",
		"body": map[string]any{"unsafe": "shape"},
	})
	if err == nil {
		t.Fatal("expected error for unsupported body type")
	}
	if !strings.Contains(err.Error(), "input 'body' must be a string or []byte") {
		t.Fatalf("unexpected error: %v", err)
	}
}
