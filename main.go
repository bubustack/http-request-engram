package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bubustack/bubu-sdk-go/engram"
	sdkruntime "github.com/bubustack/bubu-sdk-go/runtime"
)

// HTTPRequestEngram implements the BatchEngram interface to perform HTTP requests.
type HTTPRequestEngram struct{}

// Init is called once when the Engram is initialized.
func (e *HTTPRequestEngram) Init(ctx context.Context, config *engram.Config, secrets *engram.Secrets) error {
	// No initialization needed for this simple engram.
	return nil
}

// Process handles the actual execution of the Engram.
func (e *HTTPRequestEngram) Process(ctx context.Context, execCtx *engram.ExecutionContext) (*engram.Result, error) {
	inputs := execCtx.Inputs()
	logger := execCtx.Logger()

	logger.Info("Starting http-request engram processing")

	// --- 1. Get and validate inputs ---
	url, ok := inputs["url"].(string)
	if !ok || url == "" {
		return &engram.Result{Error: fmt.Errorf("input 'url' is a required string")}, nil
	}

	method, ok := inputs["method"].(string)
	if !ok {
		method = http.MethodGet // Default to GET
	}
	method = strings.ToUpper(method)

	// --- 2. Build the HTTP request ---
	var bodyReader io.Reader
	if body, ok := inputs["body"]; ok {
		if bodyStr, isString := body.(string); isString {
			bodyReader = strings.NewReader(bodyStr)
		} else {
			logger.Warn("Input 'body' was provided but was not a string. Ignoring.")
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return &engram.Result{Error: fmt.Errorf("failed to create http request: %w", err)}, nil
	}

	// Add headers
	if headers, ok := inputs["headers"].(map[string]interface{}); ok {
		for key, val := range headers {
			if valStr, isString := val.(string); isString {
				req.Header.Set(key, valStr)
			}
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "bobrapet-http-request-engram/v0.1")
	}

	// --- 3. Execute the request ---
	logger.Info("Sending HTTP request", "method", method, "url", url)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return &engram.Result{Error: fmt.Errorf("failed to execute http request: %w", err)}, nil
	}
	defer resp.Body.Close()

	// --- 4. Process the response ---
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &engram.Result{Error: fmt.Errorf("failed to read response body: %w", err)}, nil
	}

	respHeaders := make(map[string]interface{})
	for key, values := range resp.Header {
		respHeaders[key] = strings.Join(values, ", ")
	}

	// --- 5. Return the output ---
	output := map[string]interface{}{
		"statusCode": resp.StatusCode,
		"status":     resp.Status,
		"headers":    respHeaders,
		"body":       string(respBody),
	}

	logger.Info("HTTP request completed successfully", "statusCode", resp.StatusCode)

	return &engram.Result{Data: output}, nil
}

func main() {
	// The SDK runtime handles the complexity of reading inputs, updating status,
	// and managing the lifecycle of the Engram. The developer only needs to
	// provide their core business logic that implements the Engram interface.
	runtime, err := sdkruntime.New(&HTTPRequestEngram{})
	if err != nil {
		panic(err)
	}
	if err := runtime.Execute(context.Background()); err != nil {
		panic(err)
	}
}
