package engram

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"http-request/pkg/config"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaesslerAG/jsonpath"
	"github.com/bubustack/bubu-sdk-go/engram"
)

// HTTPRequestEngram implements both BatchEngram and StreamingEngram interfaces.
type HTTPRequestEngram struct {
	config  *config.Config
	secrets *engram.Secrets
}

func New() *HTTPRequestEngram {
	return &HTTPRequestEngram{}
}

// Init is called once when the Engram is initialized.
func (e *HTTPRequestEngram) Init(ctx context.Context, cfg *config.Config, secrets *engram.Secrets) error {
	e.config = cfg
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

// Process handles the batch execution of the Engram.
func (e *HTTPRequestEngram) Process(ctx context.Context, execCtx *engram.ExecutionContext, i any) (*engram.Result, error) {
	inputs, ok := i.(map[string]interface{})
	if !ok {
		return &engram.Result{Error: fmt.Errorf("invalid input type, expected map[string]interface{}, got %T", i)}, nil
	}

	logger := execCtx.Logger()
	logger.Info("Starting http-request engram batch processing")

	// If pagination is off, just do a single request.
	if e.config.Pagination == nil || e.config.Pagination.Mode == "off" {
		output, err := e.doRequest(ctx, inputs)
		if err != nil {
			return &engram.Result{Error: err}, nil
		}
		logger.Info("HTTP request completed successfully")
		return &engram.Result{Data: output}, nil
	}

	// --- Pagination Logic ---
	allResults := make([]interface{}, 0)
	var finalOutput map[string]interface{}
	// Make a deep copy of the initial inputs to avoid modifying the original map.
	currentInputs, err := deepCopyMap(inputs)
	if err != nil {
		return &engram.Result{Error: fmt.Errorf("failed to copy inputs for pagination: %w", err)}, nil
	}
	pageCount := 0
	paramState := ""
	if e.config.Pagination.UpdateParam != nil {
		paramState = e.config.Pagination.UpdateParam.InitialValue
	}

	for {
		if pageCount > 0 { // Don't modify params on the first request
			if err := e.updateRequestParams(currentInputs, paramState); err != nil {
				return &engram.Result{Error: fmt.Errorf("failed to update pagination params: %w", err)}, nil
			}
		}

		if pageCount >= e.config.Pagination.MaxPages {
			logger.Warn("Pagination stopped: reached max pages limit", "limit", e.config.Pagination.MaxPages)
			break
		}

		var output map[string]interface{}
		output, err = e.doRequest(ctx, currentInputs)
		if err != nil {
			// In pagination, an error on any page fails the whole process.
			return &engram.Result{Error: fmt.Errorf("error on page %d: %w", pageCount+1, err)}, nil
		}

		// On the last page, we'll use its full output as the base for our final result.
		finalOutput = output

		// Extract results from the current page
		var pageResults []interface{}
		bodyJSON, ok := output[e.config.Response.OutputFieldName]
		if !ok {
			logger.Warn("Pagination: could not find body in response output", "field", e.config.Response.OutputFieldName)
			break
		}

		if e.config.Pagination.ResultsPath != "" {
			v, err := jsonpath.Get(e.config.Pagination.ResultsPath, bodyJSON)
			if err != nil {
				logger.Warn("Pagination: failed to get results from JSONPath", "path", e.config.Pagination.ResultsPath, "error", err)
				break
			}
			if results, ok := v.([]interface{}); ok {
				pageResults = results
			} else {
				logger.Warn("Pagination: resultsPath did not resolve to an array", "path", e.config.Pagination.ResultsPath)
				break
			}
		} else {
			// If no path, assume the whole body is the result set
			if results, ok := bodyJSON.([]interface{}); ok {
				pageResults = results
			} else {
				logger.Warn("Pagination: body is not an array and no resultsPath was provided")
				// Add the single object to the results and stop.
				allResults = append(allResults, bodyJSON)
				break
			}
		}

		if len(pageResults) == 0 {
			logger.Info("Pagination finished: received empty result set.")
			break
		}

		allResults = append(allResults, pageResults...)
		pageCount++

		// --- Determine next request ---
		var hasNext bool
		var err error
		hasNext, paramState, err = e.prepareNextRequest(currentInputs, bodyJSON, paramState)
		if err != nil {
			return &engram.Result{Error: fmt.Errorf("failed to prepare next pagination request: %w", err)}, nil
		}
		if !hasNext {
			break
		}
	}

	// Replace the body of the final output with the aggregated results.
	if finalOutput != nil {
		finalOutput[e.config.Response.OutputFieldName] = allResults
	}

	logger.Info("HTTP request with pagination completed successfully", "pagesFetched", pageCount, "totalResults", len(allResults))
	return &engram.Result{Data: finalOutput}, nil
}

func (e *HTTPRequestEngram) prepareNextRequest(currentInputs map[string]interface{}, responseBody interface{}, currentParamState string) (bool, string, error) {
	switch e.config.Pagination.Mode {
	case "nextUrl":
		if e.config.Pagination.NextURLPath == "" {
			return false, "", fmt.Errorf("nextUrl mode selected but nextUrlPath is not configured")
		}
		nextURL, err := jsonpath.Get(e.config.Pagination.NextURLPath, responseBody)
		if err != nil || nextURL == nil {
			return false, "", nil // No next URL found, we're done.
		}
		if nextURLStr, ok := nextURL.(string); ok && nextURLStr != "" {
			currentInputs["url"] = nextURLStr
			// Remove query params if we are following a full URL
			delete(currentInputs, "params")
			return true, "", nil
		}
		return false, "", nil

	case "updateParam":
		p := e.config.Pagination.UpdateParam
		if p == nil {
			return false, "", fmt.Errorf("updateParam mode selected but no config provided")
		}

		switch p.Type {
		case "page", "offset":
			val, err := strconv.Atoi(currentParamState)
			if err != nil {
				return false, "", fmt.Errorf("could not parse current param value '%s' as integer: %w", currentParamState, err)
			}
			nextVal := val + p.Increment
			return true, strconv.Itoa(nextVal), nil
		case "cursor":
			if p.CursorPath == "" {
				return false, "", fmt.Errorf("cursor type selected but cursorPath is not configured")
			}
			nextCursor, err := jsonpath.Get(p.CursorPath, responseBody)
			if err != nil || nextCursor == nil {
				return false, "", nil // No cursor found, we're done.
			}
			if nextCursorStr, ok := nextCursor.(string); ok && nextCursorStr != "" {
				return true, nextCursorStr, nil
			}
			return false, "", nil
		}

	default:
		return false, "", nil
	}
	return false, "", nil
}

func (e *HTTPRequestEngram) updateRequestParams(inputs map[string]interface{}, paramValue string) error {
	p := e.config.Pagination.UpdateParam
	if p == nil || p.Name == "" {
		return fmt.Errorf("updateParam config is missing or invalid")
	}

	// Params are expected to be in a specific structure within the inputs map.
	params, ok := inputs["params"].(map[string]interface{})
	if !ok {
		params = make(map[string]interface{})
	}
	params[p.Name] = paramValue
	inputs["params"] = params

	return nil
}

// deepCopyMap creates a deep copy of a map[string]interface{}.
func deepCopyMap(original map[string]interface{}) (map[string]interface{}, error) {
	newMap := make(map[string]interface{})
	bytes, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(bytes, &newMap)
	if err != nil {
		return nil, err
	}
	return newMap, nil
}

// Stream handles the streaming execution of the Engram.
func (e *HTTPRequestEngram) Stream(ctx context.Context, in <-chan []byte, out chan<- []byte) error {
	interval, err := time.ParseDuration(e.config.Batching.BatchInterval)
	if err != nil {
		interval = 1 * time.Second // Fallback
		log.Printf("Invalid batchInterval '%s', falling back to 1s: %v", e.config.Batching.BatchInterval, err)
	}

	batchSize := e.config.Batching.ItemsPerBatch
	if batchSize <= 0 {
		batchSize = 50 // Fallback
	}

	var wg sync.WaitGroup
	batchCounter := 0

	for data := range in {
		// If context is cancelled, we stop accepting new items.
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(d []byte) {
			defer wg.Done()
			var inputs map[string]interface{}
			if err := json.Unmarshal(d, &inputs); err != nil {
				log.Printf("Error unmarshaling input: %v", err)
				return // Don't process this item
			}

			gCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			output, err := e.doRequest(gCtx, inputs)
			if err != nil {
				log.Printf("Error processing request: %v", err)
				return // Don't send output for this item
			}

			outputBytes, err := json.Marshal(output)
			if err != nil {
				log.Printf("Error marshaling output: %v", err)
				return // Don't send output for this item
			}
			out <- outputBytes
		}(data)

		batchCounter++

		if batchCounter >= batchSize {
			wg.Wait()        // Wait for the current batch to finish processing.
			batchCounter = 0 // Reset for the next batch.

			select {
			case <-time.After(interval):
			case <-ctx.Done():
				break
			}
		}
	}

	wg.Wait()
	return ctx.Err()
}

func (e *HTTPRequestEngram) doRequest(ctx context.Context, inputs map[string]interface{}) (map[string]interface{}, error) {
	// --- 1. Get and validate inputs ---
	url, ok := inputs["url"].(string)
	if !ok || url == "" {
		url = e.config.DefaultURL
	}
	if url == "" {
		return nil, fmt.Errorf("input 'url' is a required string")
	}

	method, ok := inputs["method"].(string)
	if !ok || method == "" {
		method = e.config.DefaultMethod
	}
	method = strings.ToUpper(method)

	// --- 2. Build the HTTP request ---
	var bodyReader io.Reader
	if body, ok := inputs["body"]; ok {
		if bodyStr, isString := body.(string); isString {
			bodyReader = strings.NewReader(bodyStr)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	// Add query params
	if params, ok := inputs["params"].(map[string]interface{}); ok {
		q := req.URL.Query()
		for key, val := range params {
			// Handle array values according to the configured format
			if arr, isArr := val.([]interface{}); isArr {
				switch e.config.QueryParams.ArrayFormat {
				case "brackets":
					for _, v := range arr {
						q.Add(key+"[]", fmt.Sprintf("%v", v))
					}
				case "indices":
					for i, v := range arr {
						q.Add(fmt.Sprintf("%s[%d]", key, i), fmt.Sprintf("%v", v))
					}
				case "noBrackets":
					fallthrough
				default:
					for _, v := range arr {
						q.Add(key, fmt.Sprintf("%v", v))
					}
				}
			} else {
				q.Add(key, fmt.Sprintf("%v", val))
			}
		}
		req.URL.RawQuery = q.Encode()
	}

	// Add headers from config
	for key, val := range e.config.DefaultHeaders {
		req.Header.Set(key, val)
	}

	// Add headers from input (overwriting config headers)
	if headers, ok := inputs["headers"].(map[string]interface{}); ok {
		for key, val := range headers {
			if valStr, isString := val.(string); isString {
				req.Header.Set(key, valStr)
			}
		}
	}

	// Apply authentication
	if e.config.Auth != nil {
		// Don't apply auth if Authorization header is already set, except for custom headers
		if req.Header.Get("Authorization") == "" || e.config.Auth.Type == "customHeader" {
			if err := e.applyAuth(req); err != nil {
				return nil, err
			}
		}
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "bubu-http-request-engram/v0.2")
	}

	// --- 3. Execute the request ---
	client, err := e.buildClient()
	if err != nil {
		return nil, fmt.Errorf("failed to build http client: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if e.config.NeverError {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return nil, fmt.Errorf("failed to execute http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if !e.config.NeverError {
			respBody, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("http request failed with status %s: %s", resp.Status, string(respBody))
		}
	}

	// --- 4. Process the response ---
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if e.config.NeverError {
			return map[string]interface{}{"error": fmt.Sprintf("failed to read response body: %v", err)}, nil
		}
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// --- 5. Return the output ---
	output := make(map[string]interface{})

	if e.config.Response.IncludeStatus {
		output["statusCode"] = resp.StatusCode
		output["status"] = resp.Status
	}

	if e.config.Response.IncludeHeaders {
		respHeaders := make(map[string]interface{})
		for key, values := range resp.Header {
			respHeaders[key] = strings.Join(values, ", ")
		}
		output["headers"] = respHeaders
	}

	outputFieldName := e.config.Response.OutputFieldName
	if outputFieldName == "" {
		outputFieldName = "body"
	}

	switch e.config.Response.Format {
	case "json":
		var jsonData interface{}
		if err := json.Unmarshal(respBody, &jsonData); err != nil {
			if e.config.NeverError {
				output[outputFieldName] = string(respBody)
				output["error"] = "failed to parse response body as JSON"
			} else {
				return nil, fmt.Errorf("failed to parse response body as JSON: %w", err)
			}
		} else {
			output[outputFieldName] = jsonData
		}
	case "text", "file":
		output[outputFieldName] = string(respBody)
	case "auto":
		fallthrough
	default:
		var jsonData interface{}
		if err := json.Unmarshal(respBody, &jsonData); err == nil {
			output[outputFieldName] = jsonData
		} else {
			output[outputFieldName] = string(respBody)
		}
	}

	return output, nil
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

		proxyUser, uOk := e.secrets.Get("proxy_username")
		proxyPass, pOk := e.secrets.Get("proxy_password")
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
		redirectCount := 0
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if redirectCount >= e.config.Redirects.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", e.config.Redirects.MaxRedirects)
			}

			if len(via) > 0 {
				lastReq := via[len(via)-1]
				if req.URL.Host != lastReq.URL.Host {
					if authHeader := lastReq.Header.Get("Authorization"); authHeader != "" {
						req.Header.Set("Authorization", authHeader)
					}
				}
			}

			redirectCount++
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
		if token, ok := e.secrets.Get("bearer_token"); ok {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "basic":
		user, uOk := e.secrets.Get("basic_username")
		pass, pOk := e.secrets.Get("basic_password")
		if uOk && pOk {
			auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
			req.Header.Set("Authorization", "Basic "+auth)
		}
	case "customHeader":
		if e.config.Auth.HeaderName == "" {
			return fmt.Errorf("auth type 'customHeader' requires 'headerName' in config")
		}
		if value, ok := e.secrets.Get("custom_header_value"); ok {
			if req.Header.Get(e.config.Auth.HeaderName) == "" {
				req.Header.Set(e.config.Auth.HeaderName, value)
			}
		}
	}
	return nil
}
