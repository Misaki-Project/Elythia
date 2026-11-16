package fxtwitterpreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestNativeVideoFetchClassifiesSourceFailures(t *testing.T) {
	const source = "https://video.twimg.com/amplify_video/1/vid/avc1/1280x720/clip.mp4?tag=29"
	cases := []struct {
		name       string
		status     int
		body       string
		transport  error
		wantReason string
		wantStatus int
		wantCode   string
	}{
		{name: "dmca", status: http.StatusForbidden, body: `{"error_code":2,"error_response":"Dmcaed"}`, wantReason: "dmca", wantStatus: 403, wantCode: "VIDEO_REMOVED_DMCA"},
		{name: "other forbidden", status: http.StatusForbidden, body: `{"error_code":1,"error_response":"Forbidden"}`, wantReason: "forbidden", wantStatus: 403, wantCode: "VIDEO_SOURCE_FORBIDDEN"},
		{name: "forbidden without json", status: http.StatusForbidden, body: `Dmcaed`, wantReason: "forbidden", wantStatus: 403, wantCode: "VIDEO_SOURCE_FORBIDDEN"},
		{name: "not found", status: http.StatusNotFound, wantReason: "gone", wantStatus: 404, wantCode: "VIDEO_SOURCE_GONE"},
		{name: "gone", status: http.StatusGone, wantReason: "gone", wantStatus: 410, wantCode: "VIDEO_SOURCE_GONE"},
		{name: "server error", status: http.StatusBadGateway, wantReason: "failed", wantStatus: 502, wantCode: "VIDEO_SOURCE_FAILED"},
		{name: "timeout", transport: timeoutNetError{}, wantReason: "timeout", wantCode: "VIDEO_SOURCE_TIMEOUT"},
		{name: "deadline", transport: context.DeadlineExceeded, wantReason: "timeout", wantCode: "VIDEO_SOURCE_TIMEOUT"},
		{name: "connection refused", transport: errors.New("connection refused"), wantReason: "failed", wantCode: "VIDEO_SOURCE_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if tc.transport != nil {
					return nil, tc.transport
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, release, err := fetchVideoWithAdmission(ctx, client, source, nativeVideoMaxBytes, newMediaAdmission(1, 1, nativeVideoMaxBytes+2))
			if release != nil {
				release()
			}
			var sourceErr *nativeVideoSourceError
			if !errors.As(err, &sourceErr) {
				t.Fatalf("err = %v, want nativeVideoSourceError", err)
			}
			if sourceErr.Reason != tc.wantReason || sourceErr.Status != tc.wantStatus {
				t.Fatalf("reason/status = %s/%d, want %s/%d", sourceErr.Reason, sourceErr.Status, tc.wantReason, tc.wantStatus)
			}

			var logs bytes.Buffer
			mapped := nativeVideoUnavailable(slog.New(slog.NewTextHandler(&logs, nil)), "1234567890", err)
			status, code := plugin.ExtractStatusError(mapped)
			if status == nil || status.Status != http.StatusNotFound || code != tc.wantCode {
				t.Fatalf("mapped = %v (%v, %q), want 404 %q", mapped, status, code, tc.wantCode)
			}
			if !strings.Contains(logs.String(), "reason="+tc.wantReason) || strings.Contains(logs.String(), "tag=29") {
				t.Fatalf("log = %q", logs.String())
			}
		})
	}
}

func TestNativeVideoUnavailableKeepsGenericErrorForOtherFailures(t *testing.T) {
	var logs bytes.Buffer
	mapped := nativeVideoUnavailable(slog.New(slog.NewTextHandler(&logs, nil)), "1234567890", errors.New("video content type is not allowed"))
	status, code := plugin.ExtractStatusError(mapped)
	if status == nil || status.Status != http.StatusNotFound || code != "" {
		t.Fatalf("mapped = %v (%v, %q)", mapped, status, code)
	}
	if !strings.Contains(logs.String(), "reason=invalid") {
		t.Fatalf("log = %q", logs.String())
	}
}

// /media/prepare の経路でも、大きさの上限超えは VIDEO_TOO_LARGE になること。
func TestNativeVideoUnavailableKeepsSizeLimitCode(t *testing.T) {
	var logs bytes.Buffer
	mapped := nativeVideoUnavailable(slog.New(slog.NewTextHandler(&logs, nil)), "1234567890", fmt.Errorf("fetch: %w", errNativeVideoTooLarge))
	status, code := plugin.ExtractStatusError(mapped)
	if status == nil || status.Status != http.StatusRequestEntityTooLarge || code != "VIDEO_TOO_LARGE" {
		t.Fatalf("mapped = %v (%v, %q)", mapped, status, code)
	}
}
