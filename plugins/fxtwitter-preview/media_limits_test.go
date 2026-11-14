// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

func TestMediaRequestLimiterSeparatesUsersAndBoundsItsWindow(t *testing.T) {
	limiter := newMediaRequestLimiter(2, 3)
	minute := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if !limiter.Allow("user-a", minute) || !limiter.Allow("user-a", minute.Add(time.Second)) {
		t.Fatal("user-a should be allowed two requests in the configured minute")
	}
	if limiter.Allow("user-a", minute.Add(2*time.Second)) {
		t.Fatal("user-a exceeded its per-user request budget")
	}
	if !limiter.Allow("user-b", minute.Add(2*time.Second)) {
		t.Fatal("user-a's request budget must not consume user-b's budget")
	}
	if limiter.Allow("user-c", minute.Add(3*time.Second)) {
		t.Fatal("the global request budget must cap the tracked user map")
	}
	if len(limiter.users) > limiter.globalLimit {
		t.Fatalf("tracked users = %d, global cap = %d", len(limiter.users), limiter.globalLimit)
	}
	if !limiter.Allow("user-c", minute.Add(time.Minute)) {
		t.Fatal("a new minute should reset the shared and per-user budgets")
	}
}

func TestMediaRequestLimiterDoesNotResetForAnOlderTimestamp(t *testing.T) {
	limiter := newMediaRequestLimiter(2, 4)
	minute := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if !limiter.Allow("user-a", minute.Add(time.Minute)) || !limiter.Allow("user-a", minute.Add(61*time.Second)) {
		t.Fatal("user-a should consume its configured request budget")
	}
	if !limiter.Allow("user-b", minute.Add(30*time.Second)) {
		t.Fatal("an out-of-order request should not prevent another user from making a request")
	}
	if limiter.Allow("user-a", minute.Add(62*time.Second)) {
		t.Fatal("an older timestamp reset user-a's active window")
	}
}

func TestMediaQueueWaitBudgetPreservesOrdinaryCardThroughput(t *testing.T) {
	minimum := 30 * time.Second
	tests := []struct {
		name       string
		requestCap int
		bufferCap  int
		userLimit  int
		maxBytes   int
		timeout    time.Duration
		want       time.Duration
	}{
		{name: "small images keep the floor", requestCap: 16, bufferCap: 128 << 20, userLimit: 4, maxBytes: 10 << 20, timeout: 5 * time.Second, want: minimum},
		{name: "four 11 second fetches fit", requestCap: 16, bufferCap: 128 << 20, userLimit: 4, maxBytes: 10 << 20, timeout: 11 * time.Second, want: 33 * time.Second},
		{name: "large reservation burst remains bounded", requestCap: 16, bufferCap: 128 << 20, userLimit: 4, maxBytes: 50 << 20, timeout: 30 * time.Second, want: 60 * time.Second},
		{name: "small reservation upper bound", requestCap: 16, bufferCap: 128 << 20, userLimit: 4, maxBytes: 1024, timeout: 30 * time.Second, want: 120 * time.Second},
		{name: "invalid capacity uses floor", requestCap: 16, bufferCap: 1024, userLimit: 4, maxBytes: 1024, timeout: time.Minute, want: minimum},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mediaQueueWaitBudget(tc.requestCap, tc.bufferCap, tc.userLimit, tc.maxBytes, tc.timeout, minimum)
			if got != tc.want {
				t.Fatalf("wait budget = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMediaQueueBudgetDrainsFourLargeImagesWithoutRejectingTheBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const maxBytes = 50 << 20
		budget := mediaQueueWaitBudget(mediaRequestCapacity, mediaBufferCapacity, mediaUserActiveLimit, maxBytes, 30*time.Second, mediaQueueWaitTimeout)
		gate := newMediaRequestGate(mediaRequestCapacity, mediaGlobalWaitLimit, mediaUserWaitLimit, mediaBufferCapacity)
		started := make(chan struct{}, 4)
		finished := make(chan error, 4)
		var workers sync.WaitGroup
		for range 4 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				waitCtx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				release, err := gate.acquire(waitCtx, "large-card-user", maxBytes)
				if err != nil {
					finished <- err
					return
				}
				started <- struct{}{}
				time.Sleep(11 * time.Second)
				release()
				finished <- nil
			}()
		}
		synctest.Wait()
		gate.mu.Lock()
		active, waiting := gate.active, len(gate.waiters)
		gate.mu.Unlock()
		if active != 1 || waiting != 3 {
			t.Fatalf("large-card gate active/waiting = %d/%d, want 1/3", active, waiting)
		}
		if len(started) != 1 {
			t.Fatalf("initial active large media = %d, want 1", len(started))
		}
		time.Sleep(44 * time.Second)
		synctest.Wait()
		workers.Wait()
		close(finished)
		for err := range finished {
			if err != nil {
				t.Fatalf("a legitimate queued image was rejected: %v", err)
			}
		}
		if len(started) != 4 {
			t.Fatalf("started large media = %d, want all 4", len(started))
		}
	})
}

func TestMediaRequestGateCancelsAndBoundsQueuedUsers(t *testing.T) {
	gate := newMediaRequestGate(2, 1, 1, 2*(1024+1))
	first, err := gate.acquire(context.Background(), "user-a", 1024)
	if err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := gate.acquire(waitCtx, "user-a", 1024)
		queued <- err
	}()
	waitForMediaGate(t, gate, 1, 1)
	other, err := gate.acquire(context.Background(), "user-b", 1024)
	if err != nil {
		t.Fatalf("one user's ineligible waiter blocked another user: %v", err)
	}
	if _, err := gate.acquire(context.Background(), "user-c", 1024); !errors.Is(err, errMediaBusy) {
		t.Fatalf("global queue bound error = %v, want busy", err)
	}
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter error = %v", err)
	}
	waitForMediaGate(t, gate, 2, 0)
	first()
	other()
}

func TestMediaRequestGateAcceptsEighteenRequestsFromOneCard(t *testing.T) {
	gate := newMediaRequestGate(mediaRequestCapacity, 256, 32, mediaBufferCapacity)
	const requests = 18
	acquired := make(chan func(), requests)
	errs := make(chan error, requests)
	for range requests {
		go func() {
			release, err := gate.acquire(context.Background(), "one-user", 10<<20)
			if err != nil {
				errs <- err
				return
			}
			acquired <- release
		}()
	}
	waitForMediaGate(t, gate, 4, requests-4)
	for range requests {
		select {
		case release := <-acquired:
			release()
		case err := <-errs:
			t.Fatalf("ordinary 18-request card was rejected: %v", err)
		case <-time.After(time.Second):
			t.Fatal("queued card media did not drain")
		}
	}
	waitForMediaGate(t, gate, 0, 0)
}

func TestMediaRequestGateLeavesOneMaximumReservationForAnotherUser(t *testing.T) {
	gate := newMediaRequestGate(16, 8, 4, 3*(1024+1))
	first, err := gate.acquire(context.Background(), "user-a", 1024)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.acquire(context.Background(), "user-a", 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := gate.acquire(ctx, "user-a", 1024)
		queued <- err
	}()
	waitForMediaGate(t, gate, 2, 1)
	other, err := gate.acquire(context.Background(), "user-b", 1024)
	if err != nil {
		t.Fatalf("a second user did not retain one shared slot: %v", err)
	}
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued same-user request was not cancelled: %v", err)
	}
	for _, release := range []func(){first, second, other} {
		release()
	}
}

func TestMediaResponseCacheCopiesExpiresAndEvictsByByteAndEntryCaps(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cache := newMediaResponseCache(5, 2, time.Minute)
	firstKey := mediaCacheKey{PostID: "1234567890", FetchedAt: now, SourceURL: "https://pbs.twimg.com/a.png", MaxBytes: 1024}
	secondKey := mediaCacheKey{PostID: "1234567891", FetchedAt: now, SourceURL: "https://pbs.twimg.com/b.png", MaxBytes: 1024}
	cache.Set(firstKey, plugin.Blob{ContentType: "image/png", Body: []byte("abcd")}, now)
	cache.Set(secondKey, plugin.Blob{ContentType: "image/png", Body: []byte("xy")}, now)
	if _, ok := cache.Get(firstKey, now); ok {
		t.Fatal("least-recently-used entry was not evicted to satisfy the byte cap")
	}
	got, ok := cache.Get(secondKey, now)
	if !ok || string(got.Body) != "xy" || len(got.Body) != 2 || cap(got.Body) != 2 {
		t.Fatalf("cached body = %#v, hit=%v; want an exact byte copy", got.Body, ok)
	}
	got.Body[0] = 'z'
	again, ok := cache.Get(secondKey, now)
	if !ok || string(again.Body) != "xy" {
		t.Fatalf("caller mutation changed cached bytes: %q", again.Body)
	}
	cache.Set(firstKey, plugin.Blob{ContentType: "image/png", Body: []byte("123456")}, now)
	if _, ok := cache.Get(firstKey, now); ok {
		t.Fatal("entry larger than the byte cap was cached")
	}
	expiringCache := newMediaResponseCache(5, 2, time.Minute)
	expiringCache.Set(secondKey, plugin.Blob{ContentType: "image/png", Body: []byte("xy")}, now)
	if _, ok := expiringCache.Get(secondKey, now.Add(time.Minute)); ok {
		t.Fatal("expired cache entry was returned")
	}
}

func waitForMediaGate(t *testing.T, gate *mediaRequestGate, active, waiting int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gate.mu.Lock()
		gotActive, gotWaiting := gate.active, len(gate.waiters)
		gate.mu.Unlock()
		if gotActive == active && gotWaiting == waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	t.Fatalf("media gate active=%d waiting=%d; want %d/%d", gate.active, len(gate.waiters), active, waiting)
}
