# HTTP Request Engram

Version: `0.1.0`

A powerful and flexible Engram for making HTTP requests. This Engram is designed to be a general-purpose tool for interacting with any REST API, webhook, or web service. It supports dual-mode execution for both single-shot jobs and long-running streaming services.

## Features

This Engram has been engineered to provide a comprehensive set of features for robust and reliable HTTP communication in a variety of scenarios.

### Core Features
- **Dual Mode:** Can be run as a `job` for single requests or as a `deployment`/`statefulset` for a persistent streaming service.
- **Flexible Authentication:**
  - **Bearer Token:** Provide a `bearer_token` via a secret.
  - **Basic Auth:** Provide `basic_username` and `basic_password` via a secret.
  - **Custom Header:** Provide a `custom_header_value` via a secret to be sent in a configured header.
- **Dynamic Inputs:** All core request parameters (`url`, `method`, `headers`, `body`, `params`) can be provided dynamically at runtime.

### Advanced Control
- **Response Handling:**
  - Conditionally include/exclude response headers and status code.
  - Control response body format (`auto-detect`, `json`, `text`).
- **Error Handling:** A `neverError` mode allows the engram to succeed even on non-2xx status codes, returning the error details in the output.
- **Timeouts:** Configure a timeout for each request.
- **Redirects:** Enable or disable following redirects and set a maximum number of redirects to follow. The engram intelligently forwards authentication headers on cross-domain redirects.
- **SSL Verification:** Option to ignore SSL certificate validation issues.
- **Proxy Support:** Route requests through an HTTP proxy, with support for proxy authentication via secrets.
- **Query Parameter Formatting:** Control how arrays are formatted in query strings (`noBrackets`, `brackets`, `indices`) for compatibility with various web frameworks.

### Job Mode Features
- **Pagination:** Automatically handle paginated APIs to fetch a complete dataset.
  - **`nextUrl` mode:** Follows a URL for the next page found in the response body.
  - **`updateParam` mode:** Automatically increments a page/offset parameter or uses a cursor from the response body for subsequent requests.

### Streaming Mode Features
- **Batching & Rate Limiting:** When in streaming mode, you can configure the number of concurrent requests (`itemsPerBatch`) and the delay between batches (`batchInterval`) to control the load on the downstream service.

## Configuration (`configSchema`)

This section details the static, instance-level configuration that can be provided when deploying an `Engram` resource from this template.

| Parameter | Type | Description |
|---|---|---|
| `defaultUrl` | `string` | A fallback URL to use if one is not provided in the runtime `inputs`. |
| `defaultMethod` | `string` | A fallback HTTP method to use. Defaults to `GET`. |
| `defaultHeaders` | `map[string]string` | A map of headers to apply to every request. Runtime headers will be merged on top. |
| `auth` | `object` | Configures the authentication strategy for the instance. |
| `auth.type` | `string` | The auth type to use. One of `bearer`, `basic`, `customHeader`. |
| `auth.headerName` | `string` | The header name to use when `auth.type` is `customHeader`. |
| `proxy` | `string` | The URL of an HTTP proxy to use. |
| `batching` | `object` | **Streaming Mode Only.** Configures request batching. |
| `batching.itemsPerBatch` | `integer` | Max number of concurrent requests. |
| `batching.batchInterval`| `string` | Delay between batches (e.g., "1s"). |
| `pagination` | `object` | **Job Mode Only.** Configures automatic pagination. |
| `pagination.mode` | `string` | The strategy to use. One of `off`, `nextUrl`, `updateParam`. |
| `pagination.resultsPath` | `string` | A JSONPath expression to find the results array in the response. |
| `pagination.maxPages` | `integer` | A safeguard to limit the number of pages fetched. |
| `...` | `...` | See `Engram.yaml` for full details on `nextUrl` and `updateParam` configuration. |
| `queryParams` | `object` | Configures query parameter formatting. |
| `queryParams.arrayFormat`| `string` | How to format arrays. One of `noBrackets`, `brackets`, `indices`. |
| `response` | `object` | Configures the output structure. |
| `response.includeHeaders`| `boolean`| If `true`, includes response headers in the output. |
| `response.includeStatus`| `boolean`| If `true`, includes status code/text in the output. |
| `response.format`| `string` | The desired format of the response body. One of `auto`, `json`, `text`, `file`. |
| `neverError` | `boolean` | If `true`, the engram will not fail on non-2xx status codes. |
| `timeout` | `string` | The timeout for the request (e.g., "10s"). |
| `redirects` | `object` | Configures redirect behavior. |
| `redirects.follow`| `boolean`| If `true`, the client will follow redirects. |
| `redirects.maxRedirects`| `integer`| The maximum number of redirects to follow. |
| `ignoreSslIssues` | `boolean` | If `true`, SSL certificate validation is skipped. |

## Secrets (`secretSchema`)

This engram can be configured with the following secrets:

| Name | Expected Keys | Description |
|---|---|---|
| `bearerToken` | `bearer_token` | For `bearer` authentication. |
| `basicAuth` | `basic_username`, `basic_password` | For `basic` authentication. |
| `customHeader`| `custom_header_value` | For `customHeader` authentication. |
| `proxyAuth` | `proxy_username`, `proxy_password` | For an authenticating proxy. |

## Inputs (`inputSchema`)

This section details the dynamic inputs that can be provided for each execution.

| Parameter | Type | Description |
|---|---|---|
| `url` | `string` | **Required.** The URL to send the request to. |
| `method` | `string` | The HTTP method. Defaults to the engram's `defaultMethod` or `GET`. |
| `params` | `map[string]interface{}` | A map of query parameters to add to the URL. |
| `headers` | `map[string]interface{}` | A map of headers to send with the request. |
| `body` | `string` | The request body. |

## Outputs (`outputSchema`)

The engram will produce an output with the following structure:

| Parameter | Type | Description |
|---|---|---|
| `statusCode` | `integer` | The HTTP status code of the response. (Optional via `response.includeStatus`) |
| `status` | `string` | The HTTP status text of the response. (Optional via `response.includeStatus`) |
| `headers` | `object` | A map of the response headers. (Optional via `response.includeHeaders`) |
| `body` | `any` | The response body, formatted according to `response.format`. |
| `error` | `string` | If `neverError` is true and an error occurs, this field will contain the error message. |
