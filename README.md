# 🌐 HTTP Request Engram

The HTTP Request Engram is a dual-mode component for bobrapet Stories. It turns declarative workflow specs into robust HTTP clients that can run once as a batch Job or continuously as a streaming Deployment. Use it to call REST APIs, webhooks, or internal services without wiring clients by hand.

## 🌟 Highlights

- **Dual execution modes** – Ships the same implementation as a batch step (`job`) or a streaming service (`deployment`/`statefulset`) with automatic backpressure.
- **Rich configuration surface** – Auth, pagination, redirects, proxy support, per-field defaults, and response shaping are all driven by Engram `spec.with`.
- **Graceful error handling** – Toggle `neverError` to surface non-2xx responses without failing the step; streaming workers respect context cancellation and propagate structured errors.
- **Pagination toolkit** – Follow `nextUrl`, increment page/offset params, or chase cursors discovered in the response.
- **Observability-ready** – Execution uses the SDK logger/tracer, and responses surface status codes, headers, and optional error metadata for downstream metrics.

## 🚀 Quick Start

```bash
# Run linting and basic tests
make lint
go test ./...

# Build a container image (defaults to ghcr.io/bubustack/http-request-engram:dev)
make docker-build
```

Drop the rendered container into a `Story` step by referencing the EngramTemplate and populating the `with` block using the configuration tables below.

## ⚙️ Configuration (`Engram.spec.with`)

### Core Settings

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `defaultURL` | `string` | Fallback URL if runtime inputs omit `url`. | `""` |
| `defaultMethod` | `string` | HTTP method when inputs omit `method`. | `"GET"` |
| `defaultHeaders` | `map[string]string` | Baseline headers merged with per-request headers. | `{}` |
| `timeout` | `string` | Client timeout (Go duration). | `"10s"` |
| `neverError` | `bool` | When true, non-2xx responses emit an `error` payload but do not fail the step. | `false` |
| `ignoreSSLIssues` | `bool` | Skip TLS verification. Use with caution. | `false` |
| `proxy` | `string` | Proxy URL, e.g. `http://user:pass@host:port`. | `""` |

### Redirect Handling

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `redirects.follow` | `bool` | Whether to follow HTTP redirects automatically. | `true` |
| `redirects.maxRedirects` | `int` | Maximum number of redirects to follow before aborting the request. | `10` |

### Authentication

| Field | Type | Description |
| --- | --- | --- |
| `auth.type` | `string` | One of `bearer`, `basic`, `customHeader`. |
| `auth.headerName` | `string` | Required when `auth.type == "customHeader"`. Used as the header name populated from secrets. |

### Pagination (`job` mode)

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `pagination.mode` | `string` | Strategy: `off`, `nextUrl`, or `updateParam`. | `"off"` |
| `pagination.resultsPath` | `string` | JSONPath to the array of results. | `""` |
| `pagination.maxPages` | `int` | Hard stop on page count. | `100` |
| `pagination.nextURLPath` | `string` | JSONPath to the next page URL when using `nextUrl`. For safety, resolved URLs must stay on the same origin (scheme + host) as the current page URL. | `""` |
| `pagination.updateParam` | `object` | Settings when mutating a query param each page. See Engram.yaml for full schema (`name`, `type`, `initialValue`, `increment`, `cursorPath`). |

### Query Parameters

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `queryParams.arrayFormat` | `string` | How to encode array params: `noBrackets`, `brackets`, or `indices`. | `"noBrackets"` |

### Response Formatting

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `response.includeHeaders` | `bool` | Include response headers in output. | `true` |
| `response.includeStatus` | `bool` | Include status code/text in output. | `true` |
| `response.format` | `string` | `auto`, `json`, `text`, or `file`. `file` returns a structured blob map with base64 payload (`encoding`, `data`, `sizeBytes`, and optional `contentType`). | `"auto"` |
| `response.outputFieldName` | `string` | Field used to store the response body. | `"body"` |

### Streaming Batching

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `batching.itemsPerBatch` | `int` | Concurrent requests before pausing. | `50` |
| `batching.batchInterval` | `string` | Sleep duration between batches. | `"1s"` |

## 🔐 Secrets

| Purpose | Expected Keys | Notes |
| --- | --- | --- |
| Bearer auth | `bearer_token` | Injects `Authorization: Bearer …` unless already present. |
| Basic auth | `basic_username`, `basic_password` | Injects an RFC 7617 header. |
| Custom header | `custom_header_value` | Writes to `auth.headerName` unless request already set it. |
| Proxy credentials | `proxy_username`, `proxy_password` | Optional when proxy URL omits credentials. |

## 📥 Inputs

| Field | Type | Description |
| --- | --- | --- |
| `url` | `string` | Target URL (overrides `defaultURL`). |
| `method` | `string` | HTTP method (overrides `defaultMethod`). |
| `params` | `map[string]any` | Query parameters; arrays obey `queryParams.arrayFormat`. |
| `headers` | `map[string]any` | Per-request headers merged over defaults. |
| `body` | `string \| []byte` | Request payload (forwarded as-is). |

## 📤 Outputs

The Engram returns a map containing:

| Field | Description |
| --- | --- |
| `statusCode`, `status` | Present when `response.includeStatus` is true. |
| `headers` | Response headers joined as comma-delimited strings when `response.includeHeaders` is true. |
| `<outputFieldName>` | Parsed response body. Defaults to `"body"`. In `response.format=file`, this is an object with `encoding`, `data`, `sizeBytes`, and optional `contentType`. |
| `error` | Only when `neverError` is enabled or body parsing fails; contains `statusCode`, `status`, and/or a `message`. |

## 🔄 Streaming Mode

Streaming consumes `engram.InboundMessage` on input and emits plain
`engram.StreamMessage` on output.

- `Inputs` carries the decoded request map when the caller already resolved structured inputs.
- `Payload` is used as a fallback when `Inputs` is empty and the request arrives as JSON.
- `Metadata` is propagated verbatim to the outbound message for tracing.
- Call `msg.Done()` after successful handling so the SDK can advance delivery acknowledgement for ordered/replay-capable transports.

The Engram processes messages concurrently (bounded by `itemsPerBatch`), honors `ctx.Done()` for shutdown, and reuses the same response shaping as batch mode.

Structured JSON streaming responses keep their canonical JSON in `Payload` and
mirror the same bytes into `Binary` with `MimeType: application/json`. Raw
`Binary` without `Payload` is reserved for opaque media or non-JSON blobs.

## 🧪 Local Development

- `make lint` – Run golangci-lint with the shared BubuStack config.
- `go test ./...` – Build/tests (no fixtures required).
- `make docker-build` – Build a container image for local clusters.

## 🧭 Observability & Error Handling

- Standard logging flows through the SDK logger available via `ExecutionContext`.
- Enable `neverError` to capture non-2xx responses for inspection without failing the Story step.
- When pagination is active, warnings provide JSONPath and state hints to simplify debugging.

## 🤝 Community & Support

- [Contributing](./CONTRIBUTING.md)
- [Support](./SUPPORT.md)
- [Security Policy](./SECURITY.md)
- [Code of Conduct](./CODE_OF_CONDUCT.md)
- [Discord](https://discord.gg/dysrB7D8H6)


## 📄 License

Copyright 2025 BubuStack.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
