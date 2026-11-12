// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

type capturedRoutes struct {
	handlers map[string]plugin.Handler
}

func (r *capturedRoutes) POST(path string, handler plugin.Handler) {
	r.handlers["POST "+path] = handler
}

func (r *capturedRoutes) GET(path string, handler plugin.Handler) {
	r.handlers["GET "+path] = handler
}

type queueOverrideContext struct {
	plugin.Context
	queue plugin.Queue
}

func (c queueOverrideContext) Queue() plugin.Queue { return c.queue }

type cancelAwareQueue struct {
	err       error
	ctxErr    error
	calls     int
	waitUntil context.Context
}

func (q *cancelAwareQueue) Enqueue(ctx context.Context, _ string, _ any, _ ...plugin.EnqueueOption) error {
	q.calls++
	q.ctxErr = ctx.Err()
	if q.ctxErr != nil {
		return q.ctxErr
	}
	if q.waitUntil != nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return q.err
}

type cancelAtQueueRequest struct {
	ctx         context.Context
	cancel      context.CancelFunc
	contextCall int
	body        string
}

func (r *cancelAtQueueRequest) Context() context.Context {
	r.contextCall++
	// The route reads the note, reads the cache, and acquires its lease before
	// it asks the queue to accept the job. Model a browser disconnect at that
	// exact boundary.
	if r.contextCall == 4 {
		r.cancel()
	}
	return r.ctx
}

func (r *cancelAtQueueRequest) Bind(v any) error       { return json.Unmarshal([]byte(r.body), v) }
func (r *cancelAtQueueRequest) BindStrict(v any) error { return json.Unmarshal([]byte(r.body), v) }
func (r *cancelAtQueueRequest) Param(string) string    { return "" }
func (r *cancelAtQueueRequest) Query(string) string    { return "" }
func (r *cancelAtQueueRequest) UserID() string         { return "u1" }
func (r *cancelAtQueueRequest) IsModerator() bool      { return false }
func (r *cancelAtQueueRequest) IsAdministrator() bool  { return false }
func (r *cancelAtQueueRequest) CheckedRolePolicies(keys ...string) (map[string]any, error) {
	policies := allowPreviewPolicies()
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := policies[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func previewRouteWithQueue(t *testing.T, queue *cancelAwareQueue) (*store, plugin.Handler, *cancelAtQueueRequest) {
	t.Helper()
	db := migratedDB(t)
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{
		"externalFetchEnabled":  true,
		"requestTimeoutSeconds": 1,
	})
	// Apply the real plugin schema and register its job declaration before using
	// the same host context with a queue that can observe request cancellation.
	h.Routes(Plugin)
	router := &capturedRoutes{handlers: make(map[string]plugin.Handler)}
	ctx := queueOverrideContext{Context: h.Context(), queue: queue}
	if err := routes(ctx, router); err != nil {
		t.Fatal(err)
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	request := &cancelAtQueueRequest{
		ctx: requestCtx, cancel: cancel,
		body: `{"i":"native-token","noteId":"note1","postId":"1234567890"}`,
	}
	t.Cleanup(cancel)
	return newStore(db, testConfig()), router.handlers["POST /preview"], request
}

func TestPreviewQueueFailureSurvivesClientCancellationAndCanRetry(t *testing.T) {
	queue := &cancelAwareQueue{err: errors.New("queue unavailable")}
	s, handler, request := previewRouteWithQueue(t, queue)
	_, err := handler(request)
	if err == nil {
		t.Fatal("queue failure was hidden")
	}
	if queue.calls != 1 || queue.ctxErr != nil {
		t.Fatalf("enqueue context = %v, calls = %d; enqueue must outlive client cancellation", queue.ctxErr, queue.calls)
	}

	entry, err := s.ReadPublic(context.Background(), "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil || entry.State == "pending" {
		t.Fatalf("failed enqueue left an orphan pending cache row: %#v", entry)
	}
	retry, err := s.AcquireFetchWithBudget(context.Background(), "1234567890", "u1", coldFetchBudget{PerHour: 100, PerDay: 600}, time.Now().UTC().Add(time.Millisecond))
	if err != nil || retry == nil {
		t.Fatalf("verified next request could not retry after enqueue failure: lease=%#v err=%v", retry, err)
	}
}

func TestPreviewQueueTimeoutUsesIndependentCleanupContext(t *testing.T) {
	queue := &cancelAwareQueue{waitUntil: context.Background()}
	s, handler, request := previewRouteWithQueue(t, queue)
	_, err := handler(request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("enqueue error = %v, want deadline exceeded", err)
	}
	if queue.calls != 1 || queue.ctxErr != nil {
		t.Fatalf("enqueue context = %v, calls = %d; request cancel must not cancel enqueue before its own timeout", queue.ctxErr, queue.calls)
	}
	entry, err := s.ReadPublic(context.Background(), "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil || entry.State == "pending" {
		t.Fatalf("enqueue timeout left an orphan pending cache row: %#v", entry)
	}
	retry, err := s.AcquireFetchWithBudget(context.Background(), "1234567890", "u1", coldFetchBudget{PerHour: 100, PerDay: 600}, time.Now().UTC().Add(time.Millisecond))
	if err != nil || retry == nil {
		t.Fatalf("verified next request could not retry after enqueue timeout: lease=%#v err=%v", retry, err)
	}
}
