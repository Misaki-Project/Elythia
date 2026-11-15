// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/imagedecode"
)

// Unit callers consume the Blob immediately. Route tests below retain the real
// incoming request context instead, including time spent writing the response.
func fetchTestMedia(ctx context.Context, client *http.Client, rawURL string, maxBytes int) (plugin.Blob, error) {
	return fetchTestMediaWithDecoder(ctx, client, rawURL, maxBytes, imagedecode.DecodeImage)
}

func fetchTestMediaWithDecoder(ctx context.Context, client *http.Client, rawURL string, maxBytes int, decode func([]byte) (image.Image, error)) (plugin.Blob, error) {
	responseCtx, done := context.WithCancel(context.Background())
	defer done()
	return fetchMediaWithDecoder(ctx, responseCtx, client, rawURL, maxBytes, decode)
}

func waitMediaRequests(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(processMediaAdmission.requests) != count {
		if time.Now().After(deadline) {
			t.Fatalf("media requests retained: got %d, want %d", len(processMediaAdmission.requests), count)
		}
		time.Sleep(time.Millisecond)
	}
}

type heldMediaBody struct {
	io.Reader
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
	closed  atomic.Bool
}

func (b *heldMediaBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.Reader.Read(p)
}

func (b *heldMediaBody) Close() error {
	b.closed.Store(true)
	return nil
}

type countedMediaReader struct {
	reads  atomic.Int32
	closed atomic.Bool
}

func (r *countedMediaReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.ErrUnexpectedEOF
}

func (r *countedMediaReader) Close() error {
	r.closed.Store(true)
	return nil
}

func TestMediaFetchWaitsBeforeBodyAndHonorsCancellation(t *testing.T) {
	// A request must reserve its maximum possible body size before making an
	// outbound request. Saturate the weighted budget without allocating bytes.
	admission := newMediaAdmission(1, 2, 256)
	if !admission.buffered.TryAcquire(256) {
		t.Fatal("could not reserve test byte budget")
	}
	var outbound atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound.Add(1)
		return response(http.StatusOK, "image/png", string(tinyImage(t, "png"))), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := fetchMediaWithAdmission(ctx, client, "https://pbs.twimg.com/media/other.png", 128, admission)
	if !errors.Is(err, context.DeadlineExceeded) || outbound.Load() != 0 {
		t.Fatalf("buffer waiter reached upstream: headers=%d err=%v", outbound.Load(), err)
	}
	admission.buffered.Release(256)
	blob, release, err := fetchMediaWithAdmission(context.Background(), client, "https://pbs.twimg.com/media/after.png", 128, admission)
	if release != nil {
		defer release()
	}
	if err != nil || outbound.Load() != 1 || len(blob.Body) == 0 {
		t.Fatalf("buffer budget was not reusable after cancellation: headers=%d err=%v", outbound.Load(), err)
	}
}

func TestMediaFetchPreservesParallelHeaderRequests(t *testing.T) {
	body := tinyImage(t, "png")
	const images = 12
	var headers atomic.Int32
	allHeaders := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if headers.Add(1) == images {
			close(allHeaders)
		}
		select {
		case <-allHeaders:
			return response(http.StatusOK, "image/png", string(body)), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	results := make(chan error, images)
	for i := range images {
		go func() {
			_, err := fetchTestMedia(ctx, client, fmt.Sprintf("https://pbs.twimg.com/media/parallel%d.png", i), 1<<20)
			results <- err
		}()
	}
	for range images {
		if err := <-results; err != nil {
			t.Errorf("parallel header request failed: %v", err)
		}
	}
	if headers.Load() != images {
		t.Errorf("only %d of %d header requests could start", headers.Load(), images)
	}
}

func TestMediaFetchPreservesOrdinaryConcurrentImageBurst(t *testing.T) {
	body := tinyImage(t, "png")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const images = 12
	results := make(chan error, images)
	for i := range images {
		go func() {
			blob, err := fetchTestMedia(ctx, client, fmt.Sprintf("https://pbs.twimg.com/media/image%d.png", i), 1<<20)
			if err == nil && (!bytes.Equal(blob.Body, body) || blob.ContentType != "image/png" || blob.CacheControl != "private, no-store") {
				err = errors.New("ordinary media response changed")
			}
			results <- err
		}()
	}
	for range images {
		if err := <-results; err != nil {
			t.Errorf("ordinary image failed: %v", err)
		}
	}
}

func TestMediaFetchBoundsRequestsBeforeOutbound(t *testing.T) {
	waitMediaRequests(t, 0)
	const admitted = mediaRequestCapacity
	releases := make([]func(), 0, admitted)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for range admitted {
		release, err := processMediaAdmission.admit(context.Background())
		if err != nil {
			t.Fatalf("ordinary admission capacity: %v", err)
		}
		releases = append(releases, release)
	}
	var outbound atomic.Int32
	body := tinyImage(t, "png")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound.Add(1)
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := fetchTestMedia(ctx, client, "https://pbs.twimg.com/media/full.png", 1<<20)
	if !errors.Is(err, errMediaBusy) || outbound.Load() != 0 {
		t.Fatalf("over-capacity request: outbound=%d err=%v", outbound.Load(), err)
	}
	// 受付枠が戻れば、同じURLも別のURLも通常どおり取得できる。
	releases[0]()
	releases[0]() // 解放は二重に呼んでも他のリクエストの枠を返さない。
	for _, rawURL := range []string{"https://pbs.twimg.com/media/full.png", "https://pbs.twimg.com/media/other.png"} {
		blob, err := fetchTestMedia(ctx, client, rawURL, 1<<20)
		if err != nil || !bytes.Equal(blob.Body, body) {
			t.Fatalf("request after capacity returned: %v", err)
		}
		waitMediaRequests(t, admitted-1)
	}
}

func TestMediaFetchHoldsProcessingUntilCanceledDecodeReturns(t *testing.T) {
	body := tinyImage(t, "png")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	entered := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := fetchTestMediaWithDecoder(ctx, client, "https://pbs.twimg.com/media/decoding.png", 1<<20, func(data []byte) (image.Image, error) {
			close(entered)
			<-release
			return imagedecode.DecodeImage(data)
		})
		firstDone <- err
	}()
	defer func() {
		close(release)
		select {
		case err := <-firstDone:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled decoder result: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("decoder did not complete after release")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("decoder did not start")
	}
	cancel()
	otherBody := &observedMediaBody{Reader: bytes.NewReader(body)}
	var otherDecode atomic.Int32
	otherClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: otherBody}, nil
	})}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer waitCancel()
	_, err := fetchTestMediaWithDecoder(waitCtx, otherClient, "https://pbs.twimg.com/media/next.png", 2<<20, func(data []byte) (image.Image, error) {
		otherDecode.Add(1)
		return imagedecode.DecodeImage(data)
	})
	if !errors.Is(err, context.DeadlineExceeded) || otherBody.reads.Load() == 0 || !otherBody.closed.Load() || otherDecode.Load() != 0 {
		t.Fatalf("canceled decoder waiter: reads=%d closed=%v decode=%d err=%v", otherBody.reads.Load(), otherBody.closed.Load(), otherDecode.Load(), err)
	}
}

type observedMediaBody struct {
	io.Reader
	reads  atomic.Int32
	closed atomic.Bool
}

func (b *observedMediaBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	return b.Reader.Read(p)
}

func (b *observedMediaBody) Close() error {
	b.closed.Store(true)
	return nil
}

type cancelAtMediaEOF struct {
	io.Reader
	cancel context.CancelFunc
	closed atomic.Bool
}

func (b *cancelAtMediaEOF) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		b.cancel()
	}
	return n, err
}

func (b *cancelAtMediaEOF) Close() error {
	b.closed.Store(true)
	return nil
}

func TestMediaFetchDoesNotStartDecodeAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &cancelAtMediaEOF{Reader: bytes.NewReader(tinyImage(t, "png")), cancel: cancel}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: body}, nil
	})}
	called := false
	_, err := fetchTestMediaWithDecoder(ctx, client, "https://pbs.twimg.com/media/canceled.png", 1<<20, func([]byte) (image.Image, error) {
		called = true
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) || called || !body.closed.Load() {
		t.Fatalf("canceled input started decode: called=%v closed=%v err=%v", called, body.closed.Load(), err)
	}
	assertMediaPipelineAvailable(t)
}

func assertMediaPipelineAvailable(t *testing.T) {
	t.Helper()
	waitMediaRequests(t, 0)
	body := tinyImage(t, "png")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	blob, err := fetchTestMedia(ctx, client, "https://pbs.twimg.com/media/recovered.png", 1<<20)
	if err != nil || !bytes.Equal(blob.Body, body) || blob.CacheControl != "private, no-store" {
		t.Fatalf("pipeline did not recover: %v", err)
	}
	waitMediaRequests(t, 0)
	releases := make([]func(), 0, mediaRequestCapacity)
	if !processMediaAdmission.buffered.TryAcquire(mediaBufferCapacity) {
		t.Fatal("pipeline lost encoded-buffer capacity")
	}
	processMediaAdmission.buffered.Release(mediaBufferCapacity)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for range mediaRequestCapacity {
		release, err := processMediaAdmission.admit(context.Background())
		if err != nil {
			t.Fatalf("pipeline lost admission capacity: %v", err)
		}
		releases = append(releases, release)
	}
}

func TestMediaFetchReleasesCapacityAfterFailures(t *testing.T) {
	png := tinyImage(t, "png")
	for _, tc := range []struct {
		name         string
		status       int
		contentType  string
		data         []byte
		maxBytes     int
		transportErr bool
		readErr      bool
		decodeErr    bool
	}{
		{name: "transport", transportErr: true},
		{name: "status", status: http.StatusForbidden, contentType: "image/png", data: png},
		{name: "mime", status: http.StatusOK, contentType: "text/html", data: png},
		{name: "read", status: http.StatusOK, contentType: "image/png", readErr: true},
		{name: "bytes", status: http.StatusOK, contentType: "image/png", data: png, maxBytes: 8},
		{name: "sniff", status: http.StatusOK, contentType: "image/jpeg", data: png},
		{name: "dimensions", status: http.StatusOK, contentType: "image/png", data: oversizedPNGHeader(100000, 100000)},
		{name: "decoder", status: http.StatusOK, contentType: "image/png", data: png, decodeErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			released := make(chan struct{})
			close(released)
			var reader io.Reader = bytes.NewReader(tc.data)
			if tc.readErr {
				reader = &countedMediaReader{}
			}
			body := &heldMediaBody{Reader: reader, entered: make(chan struct{}), release: released}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.transportErr {
					return nil, io.ErrUnexpectedEOF
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.contentType}}, Body: body}, nil
			})}
			decode := imagedecode.DecodeImage
			if tc.decodeErr {
				decode = func([]byte) (image.Image, error) { return nil, errors.New("decoder failed") }
			}
			limit := tc.maxBytes
			if limit == 0 {
				limit = 1 << 20
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := fetchTestMediaWithDecoder(ctx, client, "https://pbs.twimg.com/media/failing.png", limit, decode); err == nil {
				t.Fatal("invalid input succeeded")
			}
			if !tc.transportErr && !body.closed.Load() {
				t.Error("failed response body was not closed")
			}
			assertMediaPipelineAvailable(t)
		})
	}
}

func TestMediaFetchAlreadyCanceledDoesNotReachOutbound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected outbound work")
	})}
	_, err := fetchTestMedia(ctx, client, "https://pbs.twimg.com/media/canceled.png", 1<<20)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("canceled request: calls=%d err=%v", calls.Load(), err)
	}
	assertMediaPipelineAvailable(t)
}

type heldMediaWriter struct {
	header  http.Header
	started chan struct{}
	release <-chan struct{}
	body    []byte
}

func (w *heldMediaWriter) Header() http.Header { return w.header }
func (w *heldMediaWriter) WriteHeader(int)     {}
func (w *heldMediaWriter) Write(body []byte) (int, error) {
	w.body = body
	close(w.started)
	<-w.release
	w.body = nil
	return len(body), nil
}

func TestMediaFetchAccountsForBlockedResponseWrites(t *testing.T) {
	waitMediaRequests(t, 0)
	const responses = mediaRequestCapacity
	body := tinyImage(t, "png")
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	releaseWrites := make(chan struct{})
	done := make(chan error, responses)
	defer func() {
		close(releaseWrites)
		deadline := time.After(5 * time.Second)
		for range responses {
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("held response: %v", err)
				}
			case <-deadline:
				t.Error("response writers did not stop")
				return
			}
		}
	}()
	for range responses {
		writer := &heldMediaWriter{header: make(http.Header), started: make(chan struct{}), release: releaseWrites}
		requestCtx, requestDone := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/media", nil).WithContext(requestCtx)
		go func() {
			defer requestDone()
			fetchCtx, fetchDone := context.WithCancel(req.Context())
			blob, release, err := fetchMediaWithAdmission(fetchCtx, client, "https://pbs.twimg.com/media/held.png", 1024, processMediaAdmission)
			fetchDone() // routes' fetch deadline ends before the host writes the Blob.
			if err == nil {
				_, err = writer.Write(blob.Body)
				release()
			}
			done <- err
		}()
		select {
		case <-writer.started:
			if !bytes.Equal(writer.body, body) {
				t.Fatal("writer did not retain the encoded media")
			}
		case err := <-done:
			t.Fatalf("response could not start: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("response did not start")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := fetchTestMedia(ctx, client, "https://pbs.twimg.com/media/held.png", 1024)
	if !errors.Is(err, errMediaBusy) || calls.Load() != responses {
		t.Fatalf("blocked response bodies escaped admission: outbound=%d err=%v", calls.Load(), err)
	}
}

func TestMediaFetchBoundsBufferedResponsesBeforeRead(t *testing.T) {
	waitMediaRequests(t, 0)
	// Two maximum-size leases fit under the 128 MiB cap; a third must wait
	// before outbound headers while keeping ordinary small requests possible
	// after one lease is released. The semaphore reserves weights, not memory.
	const maxBytes = 50 << 20
	admission := newMediaAdmission(1, 4, mediaBufferCapacity)
	firstRelease, err := admission.admit(context.Background(), maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	secondRelease, err := admission.admit(context.Background(), maxBytes)
	if err != nil {
		firstRelease()
		t.Fatal(err)
	}
	defer firstRelease()
	defer secondRelease()
	body := tinyImage(t, "png")
	var headers atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		headers.Add(1)
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	waitCtx, waitDone := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer waitDone()
	_, _, err = fetchMediaWithAdmission(waitCtx, client, "https://pbs.twimg.com/media/third-large-limit.png", maxBytes, admission)
	if !errors.Is(err, context.DeadlineExceeded) || headers.Load() != 0 {
		t.Fatalf("buffer budget bypassed before outbound: headers=%d err=%v", headers.Load(), err)
	}
	firstRelease()
	small, release, err := fetchMediaWithAdmission(context.Background(), client, "https://pbs.twimg.com/media/small.png", 1<<20, admission)
	if release != nil {
		defer release()
	}
	if err != nil || !bytes.Equal(small.Body, body) || headers.Load() != 1 {
		t.Fatalf("small request failed after capacity returned: headers=%d err=%v", headers.Load(), err)
	}
}

func TestMediaFetchRejectsUnavailableResponseLifetime(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected outbound request")
	})}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, responseCtx := range []context.Context{nil, context.Background(), canceled} {
		if _, err := fetchMediaWithDecoder(context.Background(), responseCtx, client, "https://pbs.twimg.com/media/lifetime.png", 1<<20, imagedecode.DecodeImage); err == nil {
			t.Error("missing or canceled response lifetime accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("untracked response started %d outbound requests", calls.Load())
	}
	assertMediaPipelineAvailable(t)
}

func TestMediaFetchRetainsCreditsUntilHTTPHandlerReturns(t *testing.T) {
	waitMediaRequests(t, 0)
	body := tinyImage(t, "png")
	imageClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "image/png", string(body)), nil
	})}
	ready := make(chan struct{})
	release := make(chan struct{})
	serverErrors := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fetchCtx, fetchDone := context.WithCancel(req.Context())
		blob, releaseMedia, err := fetchMediaWithAdmission(fetchCtx, imageClient, "https://pbs.twimg.com/media/http.png", 1024, processMediaAdmission)
		fetchDone()
		if releaseMedia != nil {
			defer releaseMedia()
		}
		close(ready)
		<-release
		if err == nil {
			_, err = w.Write(blob.Body)
		}
		serverErrors <- err
	}))
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer srv.Close()
	defer unblock()
	clientDone := make(chan error, 1)
	go func() {
		res, err := srv.Client().Get(srv.URL)
		if err == nil {
			defer res.Body.Close()
			var got []byte
			got, err = io.ReadAll(res.Body)
			if err == nil && !bytes.Equal(got, body) {
				err = errors.New("HTTP response body changed")
			}
		}
		clientDone <- err
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler did not reach the response")
	}
	if len(processMediaAdmission.requests) != 1 {
		t.Fatal("fetch cancellation released the active HTTP response")
	}
	unblock()
	for _, result := range []<-chan error{serverErrors, clientDone} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP response did not complete")
		}
	}
	assertMediaPipelineAvailable(t)
}
