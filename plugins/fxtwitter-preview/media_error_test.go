package fxtwitterpreview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

// 画像の取得の失敗を、取得先の削除・通信の失敗と、こちらのサーバーでの処理の失敗に分ける (#62)。
func TestMediaFetchClassifiesImageFailures(t *testing.T) {
	const source = "https://pbs.twimg.com/media/example.png?name=orig"
	png := tinyImage(t, "png")
	cases := []struct {
		name        string
		status      int
		contentType string
		body        []byte
		transport   error
		maxBytes    int
		wantReason  string
		wantCode    string
	}{
		{name: "deleted", status: http.StatusNotFound, wantReason: "gone", wantCode: "MEDIA_SOURCE_GONE"},
		{name: "gone", status: http.StatusGone, wantReason: "gone", wantCode: "MEDIA_SOURCE_GONE"},
		{name: "dmca", status: http.StatusForbidden, body: []byte(`{"error_code":2,"error_response":"Dmcaed"}`), wantReason: "dmca", wantCode: "MEDIA_REMOVED_DMCA"},
		{name: "forbidden", status: http.StatusForbidden, wantReason: "forbidden", wantCode: "MEDIA_SOURCE_FORBIDDEN"},
		{name: "server error", status: http.StatusServiceUnavailable, wantReason: "failed", wantCode: "MEDIA_SOURCE_FAILED"},
		{name: "timeout", transport: timeoutNetError{}, wantReason: "timeout", wantCode: "MEDIA_SOURCE_TIMEOUT"},
		{name: "connection refused", transport: errors.New("connection refused"), wantReason: "failed", wantCode: "MEDIA_SOURCE_FAILED"},
		{name: "not an image", status: http.StatusOK, contentType: "text/html", body: []byte("<html></html>"), wantReason: "processing", wantCode: "MEDIA_PROCESSING_FAILED"},
		{name: "declared too large", status: http.StatusOK, contentType: "image/png", body: png, maxBytes: 8, wantReason: "processing", wantCode: "MEDIA_PROCESSING_FAILED"},
		{name: "type mismatch", status: http.StatusOK, contentType: "image/jpeg", body: png, wantReason: "processing", wantCode: "MEDIA_PROCESSING_FAILED"},
		{name: "broken image", status: http.StatusOK, contentType: "image/png", body: append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...), wantReason: "processing", wantCode: "MEDIA_PROCESSING_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if tc.transport != nil {
					return nil, tc.transport
				}
				header := http.Header{}
				if tc.contentType != "" {
					header.Set("Content-Type", tc.contentType)
				}
				return &http.Response{StatusCode: tc.status, Header: header, ContentLength: int64(len(tc.body)), Body: io.NopCloser(bytes.NewReader(tc.body)), Request: r}, nil
			})}
			maxBytes := tc.maxBytes
			if maxBytes == 0 {
				maxBytes = 1 << 20
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, release, err := fetchMediaWithAdmission(ctx, client, source, maxBytes, newMediaAdmission(1, 1, (1<<20)+2))
			if release != nil {
				release()
			}
			if err == nil {
				t.Fatal("fetch succeeded")
			}

			var logs bytes.Buffer
			mapped := mediaUnavailable(slog.New(slog.NewTextHandler(&logs, nil)), "1234567890", "image", err)
			status, code := plugin.ExtractStatusError(mapped)
			if status == nil || status.Status != http.StatusNotFound || code != tc.wantCode {
				t.Fatalf("mapped = %v (%v, %q), want 404 %q (err %v)", mapped, status, code, tc.wantCode, err)
			}
			if !strings.Contains(logs.String(), "reason="+tc.wantReason) || strings.Contains(logs.String(), "name=orig") {
				t.Fatalf("log = %q", logs.String())
			}
		})
	}
}

func TestMediaUnavailableKeepsGenericErrorForOtherFailures(t *testing.T) {
	var logs bytes.Buffer
	mapped := mediaUnavailable(slog.New(slog.NewTextHandler(&logs, nil)), "1234567890", "image", errors.New("media URL is not allowlisted"))
	status, code := plugin.ExtractStatusError(mapped)
	if status == nil || status.Status != http.StatusNotFound || code != "" {
		t.Fatalf("mapped = %v (%v, %q)", mapped, status, code)
	}
	if !strings.Contains(logs.String(), "reason=invalid") {
		t.Fatalf("log = %q", logs.String())
	}
}
