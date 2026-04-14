package engram

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

	sdk "github.com/bubustack/bubu-sdk-go"
)

func (e *HTTPRequestEngram) debugEnabled(ctx context.Context, logger *slog.Logger) bool {
	if sdk.DebugModeEnabled() {
		return true
	}
	if logger == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return logger.Enabled(ctx, slog.LevelDebug)
}

func (e *HTTPRequestEngram) logHTTPRequest(ctx context.Context, logger *slog.Logger, req *http.Request) {
	if !e.debugEnabled(ctx, logger) || req == nil {
		return
	}
	headerKeys := make([]string, 0, len(req.Header))
	for key := range req.Header {
		headerKeys = append(headerKeys, key)
	}
	sort.Strings(headerKeys)
	logger.Debug("http request dispatch",
		slog.String("method", req.Method),
		slog.String("url", req.URL.String()),
		slog.Int64("contentLength", req.ContentLength),
		slog.Any("headers", headerKeys),
	)
}

func (e *HTTPRequestEngram) logHTTPResponse(
	ctx context.Context,
	logger *slog.Logger,
	resp *http.Response,
	bodyBytes int,
) {
	if !e.debugEnabled(ctx, logger) || resp == nil {
		return
	}
	headerKeys := make([]string, 0, len(resp.Header))
	for key := range resp.Header {
		headerKeys = append(headerKeys, key)
	}
	sort.Strings(headerKeys)
	logger.Debug("http response received",
		slog.Int("statusCode", resp.StatusCode),
		slog.String("status", resp.Status),
		slog.Int("bodyBytes", bodyBytes),
		slog.Any("headers", headerKeys),
	)
}
