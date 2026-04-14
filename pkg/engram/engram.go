// package engram implements the core logic for the HTTP Request Engram.
// This engram is a versatile, feature-rich utility for interacting with HTTP APIs.
//
// As a masterpiece of the bobrapet ecosystem, it demonstrates several advanced
// architectural patterns:
//   - Dual-Mode Implementation: It satisfies both the BatchEngram and StreamingEngram
//     interfaces, allowing it to be used for single-shot requests (in Job mode) or
//     as a persistent, rate-limited service (in Deployment mode).
//   - Rich Configuration: It exposes a wide array of options for handling
//     authentication, pagination, proxies, and response formats, making it a true
//     "swiss army knife" for HTTP interactions.
//   - Robust Error Handling: Features like `neverError` and detailed status
//     reporting make it resilient and easy to debug in complex workflows.
package engram

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bubustack/http-request-engram/pkg/config"

	"github.com/PaesslerAG/jsonpath"
	sdk "github.com/bubustack/bubu-sdk-go"
	"github.com/bubustack/bubu-sdk-go/engram"
)

// HTTPRequestEngram implements both the sdk.BatchEngram and sdk.StreamingEngram
// interfaces, making it a dual-mode component. It holds the static configuration
// and secrets provided at initialization time.
type HTTPRequestEngram struct {
	config  config.Config
	secrets *engram.Secrets
}

var emitSignalFunc = sdk.EmitSignal

// New creates a new instance of the HTTPRequestEngram.
func New() *HTTPRequestEngram {
	return &HTTPRequestEngram{}
}

// Init is called once by the SDK runtime. It receives the static configuration
// from the EngramTemplate's `with` block and the resolved secrets. Its primary
// role is to validate the configuration and set sane defaults.
func (e *HTTPRequestEngram) Init(ctx context.Context, cfg config.Config, secrets *engram.Secrets) error {
	e.config = cfg
	if ctx == nil {
		ctx = context.Background()
	}
	if secrets == nil {
		secrets = engram.NewSecrets(ctx, map[string]string{})
	}
	e.secrets = secrets // Store secrets for use in doRequest

	// Set sane defaults for pointer-based configs
	if e.config.Response == nil {
		e.config.Response = &config.ResponseConfig{
			IncludeHeaders:  true,
			IncludeStatus:   true,
			Format:          "auto",
			OutputFieldName: "body",
		}
	}
	if e.config.Redirects == nil {
		e.config.Redirects = &config.RedirectsConfig{
			Follow:       true,
			MaxRedirects: 10,
		}
	}
	if e.config.Batching == nil {
		e.config.Batching = &config.BatchingConfig{
			ItemsPerBatch: 50,
			BatchInterval: "1s",
		}
	}
	if e.config.Pagination == nil {
		e.config.Pagination = &config.PaginationConfig{
			Mode:     "off",
			MaxPages: 100,
		}
	}
	if e.config.QueryParams == nil {
		e.config.QueryParams = &config.QueryParamsConfig{
			ArrayFormat: "noBrackets",
		}
	}

	if e.config.DefaultMethod == "" {
		e.config.DefaultMethod = http.MethodGet
	}

	return nil
}

// Process handles the batch execution of the Engram (Job mode). It is the entry
// point for single, transactional HTTP requests. This method contains the complex
// logic for handling automatic pagination, which allows it to fetch a complete
// dataset from a paginated API in a single StepRun.
func (e *HTTPRequestEngram) Process(
	ctx context.Context,
	execCtx *engram.ExecutionContext,
	i any,
) (*engram.Result, error) {
	inputs, ok := i.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid input type, expected map[string]any, got %T", i)
	}

	logger := execCtx.Logger()
	logger.Info("Starting http-request engram batch processing")

	// If pagination is off, just do a single request.
	if e.config.Pagination == nil || e.config.Pagination.Mode == "off" {
		output, err := e.doRequest(ctx, logger, inputs)
		if err != nil {
			return nil, err
		}
		logger.Info("HTTP request completed successfully")
		return engram.NewResultFrom(output), nil
	}

	// --- Pagination Logic ---
	finalOutput, err := e.runPagination(ctx, logger, inputs)
	if err != nil {
		return nil, err
	}
	return engram.NewResultFrom(finalOutput), nil
}

func (e *HTTPRequestEngram) runPagination(
	ctx context.Context,
	logger *slog.Logger,
	inputs map[string]any,
) (map[string]any, error) {
	allResults := make([]any, 0)
	var finalOutput map[string]any

	currentInputs, err := cloneInputs(inputs)
	if err != nil {
		return nil, fmt.Errorf("clone pagination inputs: %w", err)
	}

	pageCount := 0
	paramState := ""
	if e.config.Pagination.UpdateParam != nil {
		paramState = e.config.Pagination.UpdateParam.InitialValue
	}

	for {
		if pageCount > 0 {
			if err := e.updateRequestParams(currentInputs, paramState); err != nil {
				return nil, fmt.Errorf("update pagination params: %w", err)
			}
		}

		if pageCount >= e.config.Pagination.MaxPages {
			logger.Warn("Pagination stopped: reached max pages limit",
				"limit", e.config.Pagination.MaxPages,
			)
			break
		}

		output, reqErr := e.doRequest(ctx, logger, currentInputs)
		if reqErr != nil {
			return nil, fmt.Errorf("page %d request failed: %w", pageCount+1, reqErr)
		}

		finalOutput = output

		bodyJSON, ok := output[e.config.Response.OutputFieldName]
		if !ok {
			logger.Warn("Pagination: missing body in response output",
				"field", e.config.Response.OutputFieldName,
			)
			break
		}

		pageResults, done, collectErr := e.collectPageResults(bodyJSON, logger)
		if collectErr != nil {
			logger.Warn("Pagination: failed to collect page results", "error", collectErr)
			break
		}

		if len(pageResults) == 0 {
			logger.Info("Pagination finished: received empty result set.")
			break
		}

		allResults = append(allResults, pageResults...)
		if done {
			break
		}
		pageCount++

		hasNext, nextState, nextErr := e.prepareNextRequest(currentInputs, bodyJSON, paramState)
		if nextErr != nil {
			return nil, fmt.Errorf("prepare next pagination request: %w", nextErr)
		}
		paramState = nextState
		if !hasNext {
			break
		}
	}

	if finalOutput != nil {
		finalOutput[e.config.Response.OutputFieldName] = allResults
	}

	logger.Info("HTTP request with pagination completed successfully",
		"pagesFetched", pageCount,
		"totalResults", len(allResults),
	)
	return finalOutput, nil
}

func cloneInputs(src map[string]any) (map[string]any, error) {
	if src == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *HTTPRequestEngram) collectPageResults(
	body any,
	logger *slog.Logger,
) ([]any, bool, error) {
	if e.config.Pagination.ResultsPath != "" {
		v, err := jsonpath.Get(e.config.Pagination.ResultsPath, body)
		if err != nil {
			return nil, true, fmt.Errorf("jsonpath %s: %w", e.config.Pagination.ResultsPath, err)
		}
		results, ok := v.([]any)
		if !ok {
			return nil, true, fmt.Errorf("resultsPath did not resolve to an array")
		}
		return results, false, nil
	}

	if results, ok := body.([]any); ok {
		return results, false, nil
	}

	logger.Warn("Pagination: body is not an array and no resultsPath was provided")
	return []any{body}, true, nil
}

func (e *HTTPRequestEngram) prepareNextRequest(
	currentInputs map[string]any,
	responseBody any,
	currentParamState string,
) (bool, string, error) {
	switch e.config.Pagination.Mode {
	case "nextUrl":
		return e.prepareNextURLRequest(currentInputs, responseBody)
	case "updateParam":
		return e.prepareUpdateParamRequest(responseBody, currentParamState)
	default:
		return false, "", nil
	}
}

func (e *HTTPRequestEngram) prepareNextURLRequest(
	currentInputs map[string]any,
	responseBody any,
) (bool, string, error) {
	if e.config.Pagination.NextURLPath == "" {
		return false, "", fmt.Errorf("nextUrl mode selected but nextUrlPath is not configured")
	}

	nextURL, err := jsonpath.Get(e.config.Pagination.NextURLPath, responseBody)
	if err != nil || nextURL == nil {
		return false, "", nil
	}

	nextURLStr, ok := nextURL.(string)
	if !ok || nextURLStr == "" {
		return false, "", nil
	}

	currentURLRaw, err := e.resolveURL(currentInputs)
	if err != nil {
		return false, "", fmt.Errorf("resolve current page URL: %w", err)
	}
	currentURL, err := url.Parse(currentURLRaw)
	if err != nil {
		return false, "", fmt.Errorf("parse current page URL %q: %w", currentURLRaw, err)
	}
	nextParsed, err := url.Parse(nextURLStr)
	if err != nil {
		return false, "", fmt.Errorf("parse next page URL %q: %w", nextURLStr, err)
	}
	resolvedNext := currentURL.ResolveReference(nextParsed)
	if !sameOrigin(currentURL, resolvedNext) {
		return false, "", fmt.Errorf(
			"pagination nextUrl origin change is not allowed: %s -> %s",
			currentURL.Host,
			resolvedNext.Host,
		)
	}

	currentInputs["url"] = resolvedNext.String()
	delete(currentInputs, "params")
	return true, "", nil
}

func sameOrigin(base *url.URL, next *url.URL) bool {
	if base == nil || next == nil {
		return false
	}
	return strings.EqualFold(base.Scheme, next.Scheme) && strings.EqualFold(base.Host, next.Host)
}

func (e *HTTPRequestEngram) prepareUpdateParamRequest(
	responseBody any,
	currentParamState string,
) (bool, string, error) {
	p := e.config.Pagination.UpdateParam
	if p == nil {
		return false, "", fmt.Errorf("updateParam mode selected but no config provided")
	}

	switch p.Type {
	case "page", "offset":
		val, err := strconv.Atoi(currentParamState)
		if err != nil {
			return false, "", fmt.Errorf("parse current param value '%s': %w", currentParamState, err)
		}
		nextVal := val + p.Increment
		return true, strconv.Itoa(nextVal), nil
	case "cursor":
		if p.CursorPath == "" {
			return false, "", fmt.Errorf("cursor type selected but cursorPath is not configured")
		}
		nextCursor, err := jsonpath.Get(p.CursorPath, responseBody)
		if err != nil || nextCursor == nil {
			return false, "", nil
		}
		nextCursorStr, ok := nextCursor.(string)
		if !ok || nextCursorStr == "" {
			return false, "", nil
		}
		return true, nextCursorStr, nil
	default:
		return false, "", fmt.Errorf("unsupported updateParam type %q", p.Type)
	}
}

func (e *HTTPRequestEngram) updateRequestParams(inputs map[string]any, paramValue string) error {
	p := e.config.Pagination.UpdateParam
	if p == nil || p.Name == "" {
		return fmt.Errorf("updateParam config is missing or invalid")
	}

	// Params are expected to be in a specific structure within the inputs map.
	params, ok := inputs["params"].(map[string]any)
	if !ok {
		params = make(map[string]any)
	}
	params[p.Name] = paramValue
	inputs["params"] = params

	return nil
}

// Stream handles the streaming execution of the Engram (Deployment mode). It runs
// a persistent loop that receives input data over a channel, makes HTTP requests,
// and sends the results back on an output channel. This implementation includes
// sophisticated batching and rate-limiting logic to avoid overwhelming a downstream API.
func (e *HTTPRequestEngram) Stream(
	ctx context.Context,
	in <-chan engram.InboundMessage,
	out chan<- engram.StreamMessage,
) error {
	logger := sdk.LoggerFromContext(ctx).With(
		"component", "http-request-engram",
		"mode", "stream",
	)
	interval, err := time.ParseDuration(e.config.Batching.BatchInterval)
	if err != nil || interval <= 0 {
		interval = time.Second
		logger.Warn("invalid batch interval, using 1s fallback",
			slog.String("configured", e.config.Batching.BatchInterval),
			slog.Any("parseError", err),
		)
	}

	batchSize := e.config.Batching.ItemsPerBatch
	if batchSize <= 0 {
		batchSize = 50
	}

	var (
		wg         sync.WaitGroup
		batchCount int
	)
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	errCh := make(chan error, 1)

	for {
		select {
		case err := <-errCh:
			cancelWorkers()
			wg.Wait()
			return err
		case <-ctx.Done():
			cancelWorkers()
			wg.Wait()
			return ctx.Err()
		case msg, ok := <-in:
			if !ok {
				cancelWorkers()
				wg.Wait()
				return nil
			}

			e.launchStreamWorker(workerCtx, logger, &wg, msg, out, errCh)
			batchCount++

			if batchCount >= batchSize {
				if err := e.waitForBatchDrain(ctx, &wg, interval); err != nil {
					return err
				}
				batchCount = 0
			}
		}
	}
}

func (e *HTTPRequestEngram) launchStreamWorker(
	ctx context.Context,
	logger *slog.Logger,
	wg *sync.WaitGroup,
	msg engram.InboundMessage,
	out chan<- engram.StreamMessage,
	errCh chan<- error,
) {
	message := cloneStreamMessage(msg)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer message.Done()

		response, emit, err := e.processStreamMessage(ctx, logger, message)
		if err != nil {
			if ctx.Err() == nil {
				select {
				case errCh <- err:
				default:
				}
			}
			return
		}
		if !emit {
			return
		}

		select {
		case out <- response:
		case <-ctx.Done():
		}
	}()
}

func (e *HTTPRequestEngram) waitForBatchDrain(ctx context.Context, wg *sync.WaitGroup, interval time.Duration) error {
	wg.Wait()
	select {
	case <-time.After(interval):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *HTTPRequestEngram) processStreamMessage(
	ctx context.Context,
	logger *slog.Logger,
	message engram.InboundMessage,
) (engram.StreamMessage, bool, error) {
	inputs, err := decodeStreamInputs(message)
	if err != nil {
		return engram.StreamMessage{}, false, err
	}
	if inputs == nil {
		if logger != nil {
			logger.Warn("stream message missing inputs and payload; skipping",
				slog.Any("metadata", message.Metadata),
			)
		}
		return engram.StreamMessage{}, false, nil
	}

	output, err := e.doRequest(ctx, logger, inputs)
	if err != nil {
		return engram.StreamMessage{}, false, err
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		return engram.StreamMessage{}, false, fmt.Errorf("marshal stream output: %w", err)
	}

	return engram.StreamMessage{
		Metadata: cloneStreamMetadata(message.Metadata),
		Payload:  encoded,
		Binary: &engram.BinaryFrame{
			Payload:  encoded,
			MimeType: "application/json",
		},
	}, true, nil
}

func decodeStreamInputs(message engram.InboundMessage) (map[string]any, error) {
	raw := message.Inputs
	if len(raw) == 0 && len(message.Payload) > 0 {
		raw = message.Payload
	}
	if len(raw) == 0 && message.Binary != nil {
		raw = message.Binary.Payload
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var inputs map[string]any
	if err := json.Unmarshal(raw, &inputs); err != nil {
		return nil, fmt.Errorf("unmarshal stream inputs: %w", err)
	}
	return inputs, nil
}

func cloneStreamMessage(msg engram.InboundMessage) engram.InboundMessage {
	clone := msg
	clone.Metadata = cloneStreamMetadata(msg.Metadata)
	if msg.Binary != nil && len(msg.Binary.Payload) > 0 {
		clone.Binary = &engram.BinaryFrame{
			Payload:  append([]byte(nil), msg.Binary.Payload...),
			MimeType: msg.Binary.MimeType,
		}
	} else {
		clone.Binary = nil
	}
	if len(msg.Inputs) > 0 {
		clone.Inputs = make([]byte, len(msg.Inputs))
		copy(clone.Inputs, msg.Inputs)
	} else {
		clone.Inputs = nil
	}
	if len(msg.Payload) > 0 {
		clone.Payload = make([]byte, len(msg.Payload))
		copy(clone.Payload, msg.Payload)
	} else {
		clone.Payload = nil
	}
	return clone
}

func cloneStreamMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

// doRequest is the heart of the engram, containing the logic for a single HTTP
// request. It is called by both `Process` and `Stream`. This function is
// responsible for:
//  1. Assembling the request: Merging default and runtime URLs, headers, and params.
//  2. Applying authentication: Injecting bearer tokens, basic auth, or custom headers.
//  3. Building the HTTP client: Configuring timeouts, proxies, and redirect policies.
//  4. Executing the request and processing the response into the final output structure.
func (e *HTTPRequestEngram) doRequest(
	ctx context.Context,
	logger *slog.Logger,
	inputs map[string]any,
) (map[string]any, error) {
	if logger == nil {
		logger = sdk.LoggerFromContext(ctx)
	}
	start := time.Now()
	req, err := e.buildHTTPRequest(ctx, inputs)
	if err != nil {
		return nil, err
	}
	e.logHTTPRequest(ctx, logger, req)

	client, err := e.buildClient()
	if err != nil {
		return nil, fmt.Errorf("failed to build http client: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if e.config.NeverError {
			return map[string]any{"error": err.Error()}, nil
		}
		return nil, fmt.Errorf("failed to execute http request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("error closing HTTP response body", slog.Any("error", cerr))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if e.config.NeverError {
			return map[string]any{"error": fmt.Sprintf("failed to read response body: %v", err)}, nil
		}
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if !e.config.NeverError {
			return nil, fmt.Errorf("http request failed with status %s: %s", resp.Status, string(body))
		}
	}
	e.logHTTPResponse(ctx, logger, resp, len(body))

	output, err := e.buildResponsePayload(resp, body)
	if err != nil {
		return nil, err
	}
	e.emitResponseSignal(ctx, logger, req, resp, len(body), time.Since(start), output)
	return output, nil
}

func (e *HTTPRequestEngram) buildHTTPRequest(
	ctx context.Context,
	inputs map[string]any,
) (*http.Request, error) {
	targetURL, err := e.resolveURL(inputs)
	if err != nil {
		return nil, err
	}

	method := e.resolveMethod(inputs)
	bodyReader, err := prepareBodyReader(inputs)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	e.applyQueryParams(req, inputs)
	e.applyHeaders(req, inputs)
	e.ensureUserAgent(req)

	if e.shouldApplyAuth(req) {
		if err := e.applyAuth(req); err != nil {
			return nil, err
		}
	}

	return req, nil
}

func (e *HTTPRequestEngram) resolveURL(inputs map[string]any) (string, error) {
	if raw, ok := inputs["url"].(string); ok && raw != "" {
		return raw, nil
	}
	if e.config.DefaultURL != "" {
		return e.config.DefaultURL, nil
	}
	return "", fmt.Errorf("input 'url' is a required string")
}

func (e *HTTPRequestEngram) resolveMethod(inputs map[string]any) string {
	if method, ok := inputs["method"].(string); ok && method != "" {
		return strings.ToUpper(method)
	}
	return strings.ToUpper(e.config.DefaultMethod)
}

func prepareBodyReader(inputs map[string]any) (io.Reader, error) {
	body, ok := inputs["body"]
	if !ok || body == nil {
		return nil, nil
	}
	switch typed := body.(type) {
	case string:
		return strings.NewReader(typed), nil
	case []byte:
		return bytes.NewReader(typed), nil
	default:
		return nil, fmt.Errorf("input 'body' must be a string or []byte, got %T", body)
	}
}

func (e *HTTPRequestEngram) applyQueryParams(req *http.Request, inputs map[string]any) {
	params, ok := inputs["params"].(map[string]any)
	if !ok || len(params) == 0 {
		return
	}

	query := req.URL.Query()
	for key, val := range params {
		if arr, ok := val.([]any); ok {
			e.appendArrayParam(query, key, arr)
			continue
		}
		query.Add(key, fmt.Sprintf("%v", val))
	}
	req.URL.RawQuery = query.Encode()
}

func (e *HTTPRequestEngram) appendArrayParam(values url.Values, key string, arr []any) {
	for idx, item := range arr {
		value := fmt.Sprintf("%v", item)
		switch e.config.QueryParams.ArrayFormat {
		case "brackets":
			values.Add(key+"[]", value)
		case "indices":
			values.Add(fmt.Sprintf("%s[%d]", key, idx), value)
		default:
			values.Add(key, value)
		}
	}
}

func (e *HTTPRequestEngram) applyHeaders(req *http.Request, inputs map[string]any) {
	for key, val := range e.config.DefaultHeaders {
		req.Header.Set(key, val)
	}

	headers, ok := inputs["headers"].(map[string]any)
	if !ok {
		return
	}
	for key, val := range headers {
		if strVal, ok := val.(string); ok {
			req.Header.Set(key, strVal)
		}
	}
}

func (e *HTTPRequestEngram) ensureUserAgent(req *http.Request) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "bubu-http-request-engram/v0.2")
	}
}

func (e *HTTPRequestEngram) shouldApplyAuth(req *http.Request) bool {
	if e.config.Auth == nil {
		return false
	}
	if e.config.Auth.Type == "customHeader" {
		return true
	}
	return req.Header.Get("Authorization") == ""
}

func (e *HTTPRequestEngram) buildResponsePayload(resp *http.Response, body []byte) (map[string]any, error) {
	output := make(map[string]any)

	if e.config.Response.IncludeStatus {
		output["statusCode"] = resp.StatusCode
		output["status"] = resp.Status
	}

	if e.config.Response.IncludeHeaders {
		output["headers"] = flattenHeaders(resp.Header)
	}

	field := e.outputFieldName()
	value, err := e.formatResponseBody(resp, body)
	if err != nil {
		if !e.config.NeverError {
			return nil, err
		}
		output[field] = string(body)
		output["error"] = map[string]any{"message": err.Error()}
	} else {
		output[field] = value
	}

	if resp.StatusCode >= http.StatusBadRequest {
		message := strings.TrimSpace(string(body))
		if e.config.NeverError {
			output["error"] = map[string]any{
				"statusCode": resp.StatusCode,
				"status":     resp.Status,
				"message":    message,
			}
		} else {
			return nil, fmt.Errorf("http request failed with status %s: %s", resp.Status, message)
		}
	}

	return output, nil
}

func (e *HTTPRequestEngram) formatResponseBody(resp *http.Response, body []byte) (any, error) {
	switch e.config.Response.Format {
	case "json":
		var out any
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("failed to parse response body as JSON: %w", err)
		}
		return out, nil
	case "text":
		return string(body), nil
	case "file":
		file := map[string]any{
			"encoding":  "base64",
			"data":      base64.StdEncoding.EncodeToString(body),
			"sizeBytes": len(body),
		}
		if resp != nil {
			if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
				file["contentType"] = contentType
			}
		}
		return file, nil
	case "auto":
		var out any
		if err := json.Unmarshal(body, &out); err == nil {
			return out, nil
		}
		return string(body), nil
	default:
		return string(body), nil
	}
}

func (e *HTTPRequestEngram) emitResponseSignal(
	ctx context.Context,
	logger *slog.Logger,
	req *http.Request,
	resp *http.Response,
	bodyBytes int,
	latency time.Duration,
	output map[string]any,
) {
	if req == nil || resp == nil {
		return
	}
	payload := map[string]any{
		"method":     strings.ToUpper(req.Method),
		"url":        req.URL.String(),
		"statusCode": resp.StatusCode,
		"status":     resp.Status,
		"durationMs": latency.Milliseconds(),
		"bodyBytes":  bodyBytes,
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		payload["contentType"] = ct
	}
	if output != nil {
		if errVal, ok := output["error"]; ok {
			payload["error"] = errVal
		}
	}
	if err := emitSignalFunc(ctx, "http.response", payload); err != nil && !errors.Is(err, sdk.ErrSignalsUnavailable) {
		if logger == nil {
			logger = sdk.LoggerFromContext(ctx)
		}
		logger.Warn("http-request: failed to emit response signal", slog.Any("error", err))
	}
}

func (e *HTTPRequestEngram) outputFieldName() string {
	if e.config.Response.OutputFieldName != "" {
		return e.config.Response.OutputFieldName
	}
	return "body"
}

func flattenHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		out[key] = strings.Join(values, ", ")
	}
	return out
}

func (e *HTTPRequestEngram) buildClient() (*http.Client, error) {
	timeout, err := time.ParseDuration(e.config.Timeout)
	if err != nil {
		timeout = 10 * time.Second
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: e.config.IgnoreSSLIssues},
	}

	if e.config.Proxy != "" {
		proxyURL, err := url.Parse(e.config.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}

		proxyUser, uOk := e.getSecret("proxy_username")
		proxyPass, pOk := e.getSecret("proxy_password")
		if uOk && pOk {
			proxyURL.User = url.UserPassword(proxyUser, proxyPass)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	if e.config.Redirects != nil && !e.config.Redirects.Follow {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else if e.config.Redirects != nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= e.config.Redirects.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", e.config.Redirects.MaxRedirects)
			}

			// Prevent credential leakage on cross-domain redirects.
			if len(via) > 0 {
				lastReq := via[len(via)-1]
				if req.URL.Host != lastReq.URL.Host {
					req.Header.Del("Authorization")
				}
			}
			return nil
		}
	}

	return client, nil
}

func (e *HTTPRequestEngram) applyAuth(req *http.Request) error {
	if e.config.Auth == nil {
		return nil
	}

	switch e.config.Auth.Type {
	case "bearer":
		if token, ok := e.getSecret("bearer_token"); ok {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "basic":
		user, uOk := e.getSecret("basic_username")
		pass, pOk := e.getSecret("basic_password")
		if uOk && pOk {
			auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
			req.Header.Set("Authorization", "Basic "+auth)
		}
	case "customHeader":
		if e.config.Auth.HeaderName == "" {
			return fmt.Errorf("auth type 'customHeader' requires 'headerName' in config")
		}
		if value, ok := e.getSecret("custom_header_value"); ok {
			if req.Header.Get(e.config.Auth.HeaderName) == "" {
				req.Header.Set(e.config.Auth.HeaderName, value)
			}
		}
	}
	return nil
}

func (e *HTTPRequestEngram) getSecret(key string) (string, bool) {
	if e == nil || e.secrets == nil {
		return "", false
	}
	return e.secrets.Get(key)
}
