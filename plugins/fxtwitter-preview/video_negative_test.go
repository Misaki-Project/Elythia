// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin/plugintest"
)

func TestFetchVideoRejectsUnsafeResponsesAndReleasesAdmission(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
	}{
		{name: "redirect", resp: func() *http.Response {
			r := response(http.StatusFound, "video/mp4", "")
			r.Header.Set("Location", "https://video.twimg.com/other.mp4")
			return r
		}()},
		{name: "unsafe mime", resp: response(http.StatusOK, "application/octet-stream", string(testVideoMP4FTYP()))},
		{name: "invalid ftyp", resp: response(http.StatusOK, "video/mp4", strings.Repeat("x", len(testVideoMP4FTYP())))},
		{name: "byte limit", resp: func() *http.Response {
			r := response(http.StatusOK, "video/mp4", string(testVideoMP4FTYP())+"x")
			r.ContentLength = -1
			return r
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			admission := newMediaAdmission(1, 1, 64)
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return tc.resp, nil })}
			if _, release, err := fetchVideoWithAdmission(context.Background(), client, "https://video.twimg.com/clip.mp4", 16, admission); err == nil || release != nil {
				t.Fatalf("unsafe response = release:%v err:%v", release != nil, err)
			}
			valid := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, "video/mp4", string(testVideoMP4FTYP())), nil
			})}
			_, release, err := fetchVideoWithAdmission(context.Background(), valid, "https://video.twimg.com/clip.mp4", 16, admission)
			if err != nil || release == nil {
				t.Fatalf("admission remained occupied after rejected response: release:%v err:%v", release != nil, err)
			}
			release()
		})
	}
}

func TestNativeVideoResponseHoldsRequestAndByteReservationsUntilCanceled(t *testing.T) {
	admission := newMediaAdmission(1, 1, 64)
	requestGate := newMediaRequestGate(1, 2, 2, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	releaseUser, err := requestGate.acquire(ctx, "user-one", 16)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "video/mp4", string(testVideoMP4FTYP())), nil
	})}
	_, releaseBytes, err := fetchVideoWithAdmission(ctx, client, "https://video.twimg.com/clip.mp4", 16, admission)
	if err != nil || releaseBytes == nil {
		t.Fatalf("first video response = release:%v err:%v", releaseBytes != nil, err)
	}
	releaseMediaWhenRequestEnds(ctx, releaseBytes, releaseUser)

	requestGate.mu.Lock()
	gateStillReserved := requestGate.active == 1
	requestGate.mu.Unlock()
	if !gateStillReserved {
		t.Fatal("request gate released before the active response ended")
	}
	if len(admission.requests) != 1 {
		t.Fatal("byte admission released before the active response ended")
	}
	cancel()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	releaseNext, err := requestGate.acquire(waitCtx, "user-two", 16)
	if err != nil {
		t.Fatalf("request gate was not released at request end: %v", err)
	}
	releaseNext()
	releaseNextBytes, err := admission.admitWithQueue(waitCtx, 16, true)
	if err != nil {
		t.Fatalf("byte reservation was not released at request end: %v", err)
	}
	releaseNextBytes()
}

func insertLegacyVideoCache(t *testing.T, s *store, postID, payload string, now time.Time) {
	t.Helper()
	_, err := s.db.Exec(`
		INSERT INTO public_post_cache (post_id, state, payload, fetched_at, next_fetch_at, last_success_payload, last_success_at)
		VALUES ($1, 'ready', $2::jsonb, $3, $4, $2::jsonb, $3)
	`, postID, payload, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeMetadataRefreshSupportsQuoteOnlyAndPreservesTrustedStaleOnBudgetedFailure(t *testing.T) {
	s, now := testStore(t)
	s.cfg.VideoPlaybackMode = "nativeProxy"
	insertLegacyVideoCache(t, s, "1234567890", `{"hasVideo":false,"quote":{"hasVideo":true,"videos":[{"provider":"x"}]}}`, now)
	insertLegacyVideoCache(t, s, "2234567890", `{"hasVideo":false,"quote":{"hasVideo":true,"videos":[{"provider":"x"}]}}`, now)
	insertLegacyVideoCache(t, s, "3234567890", `{"hasVideo":false,"quote":{"hasVideo":true,"videos":[{"provider":"youtube"}]}}`, now)
	for _, id := range []string{"1234567890", "2234567890"} {
		needs, err := s.NeedsNativeVideoMetadata(context.Background(), id)
		if err != nil || !needs {
			t.Fatalf("quote-only native metadata need for %s = %v, %v", id, needs, err)
		}
	}
	if needs, err := s.NeedsNativeVideoMetadata(context.Background(), "3234567890"); err != nil || needs {
		t.Fatalf("YouTube-only quote incorrectly required native metadata: %v, %v", needs, err)
	}
	limits := coldFetchBudget{PerHour: 1, PerDay: 1}
	lease, err := s.AcquireNativeMetadataRefreshWithBudget(context.Background(), "1234567890", "refresh-user", limits, now)
	if err != nil || lease == nil {
		t.Fatalf("quote-only refresh lease = %#v, %v", lease, err)
	}
	if _, err := s.AcquireNativeMetadataRefreshWithBudget(context.Background(), "2234567890", "refresh-user", limits, now); err != errRateLimited {
		t.Fatalf("second refresh did not enforce checked budget: %v", err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *lease, "network", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), "1234567890")
	if err != nil || entry == nil || !entry.Stale || entry.Post == nil || entry.Post.Quote == nil {
		t.Fatalf("trusted stale quote was not retained after transient refresh failure: %#v, %v", entry, err)
	}
}

func TestNativeVideoRouteKillSwitchAndModeBlockBeforeNoteLookup(t *testing.T) {
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound++
		return response(http.StatusOK, "video/mp4", string(testVideoMP4FTYP())), nil
	})}
	for _, config := range []map[string]any{
		{"externalFetchEnabled": false, "videoPlaybackMode": "nativeProxy"},
		{"externalFetchEnabled": true, "videoPlaybackMode": "linkOnly"},
	} {
		routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(migratedDB(t)).WithAPI(api).WithHTTPClient(client).WithConfig(config).Routes(Plugin)
		_, err := routes.Call(t, "GET /media/video/:postId/:target/:index", plugintest.Request{
			UserID: "u1", Params: map[string]string{"postId": "1234567890", "target": "main", "index": "0"},
			Query: map[string]string{"noteId": "note1", "token": strings.Repeat("a", 32)}, CheckedRolePolicies: allowPreviewPolicies(),
		})
		if err == nil {
			t.Fatalf("video route accepted disabled config %v", config)
		}
	}
	if api.endpoint != "" || outbound != 0 {
		t.Fatalf("disabled native playback performed note lookup or fetch: endpoint=%q outbound=%d", api.endpoint, outbound)
	}
}

func TestNativeVideoRouteRechecksCurrentDescriptorAfterFetch(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	cfg := testConfig()
	cfg.VideoPlaybackMode = "nativeProxy"
	s := newStore(db, cfg)
	lease, err := s.AcquireFetch(context.Background(), "4234567890", "descriptor-user", now)
	if err != nil || lease == nil {
		t.Fatalf("lease = %#v, %v", lease, err)
	}
	post := PublicPost{
		ID: lease.PostID, HasVideo: true, Video: &PublicVideo{Provider: "x"},
		Videos:              []PublicVideoMetadata{{Provider: "x"}},
		privateVideoSources: privateVideoSources{Version: 1, Main: []privateVideoSource{{URL: "https://video.twimg.com/old.mp4"}}},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), post.ID)
	if err != nil || entry == nil || entry.PlaybackToken == "" {
		t.Fatalf("entry=%#v err=%v", entry, err)
	}
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/4234567890"}`}
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound++
		if _, err := db.Exec(`UPDATE public_post_cache SET payload = jsonb_set(payload, '{_fxVideoSources,main,0,url}', to_jsonb('https://video.twimg.com/new.mp4'::text), false) WHERE post_id = $1`, post.ID); err != nil {
			t.Fatalf("mutate descriptor during fetch: %v", err)
		}
		return response(http.StatusOK, "video/mp4", string(testVideoMP4FTYP())), nil
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true, "videoPlaybackMode": "nativeProxy",
	}).Routes(Plugin)
	_, err = routes.Call(t, "GET /media/video/:postId/:target/:index", plugintest.Request{
		UserID: "descriptor-user", Params: map[string]string{"postId": post.ID, "target": "main", "index": "0"},
		Query: map[string]string{"noteId": "note1", "token": entry.PlaybackToken}, CheckedRolePolicies: allowPreviewPolicies(),
	})
	if err == nil || outbound != 1 {
		t.Fatalf("changed descriptor response = err:%v outbound:%d", err, outbound)
	}
}
