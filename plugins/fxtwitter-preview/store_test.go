// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*store, time.Time) {
	t.Helper()
	cfg := testConfig()
	cfg.AuthenticatedBudget, cfg.GlobalBudget = 10, 10
	return newStore(migratedDB(t), cfg), time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
}

func TestStoreLeaseSingleflightAndSeparatedAudit(t *testing.T) {
	s, now := testStore(t)
	first, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || first == nil {
		t.Fatalf("first lease = %#v, %v", first, err)
	}
	second, err := s.AcquireFetch(context.Background(), "1234567890", "u2", now)
	if err != nil || second != nil {
		t.Fatalf("second lease = %#v, %v", second, err)
	}
	post := PublicPost{ID: first.PostID, URL: "https://x.com/i/status/1234567890", Text: "audit me", Author: PublicAuthor{Name: "A", ScreenName: "a"}, Images: []PublicImage{}}
	if updated, err := s.CompleteReady(context.Background(), *first, post, now.Add(time.Second)); err != nil || !updated {
		t.Fatalf("complete ready = %v, %v", updated, err)
	}
	entry, err := s.ReadPublic(context.Background(), first.PostID)
	if err != nil || entry == nil || entry.State != "ready" || entry.Post.Text != "audit me" {
		t.Fatalf("public entry = %#v, %v", entry, err)
	}
	items, err := s.ListAudit(context.Background(), first.PostID)
	if err != nil || len(items) != 1 || items[0].Body != "audit me" {
		t.Fatalf("audit items = %#v, %v", items, err)
	}
	stale := *first
	stale.Token = "stale"
	if updated, err := s.CompleteUnavailable(context.Background(), stale, "network", now); err != nil || updated {
		t.Fatalf("stale completion = %v, %v", updated, err)
	}
}

func TestStoreConcurrentLeaseHasOneWinnerAcrossStores(t *testing.T) {
	db := migratedDB(t)
	cfg := testConfig()
	cfg.AuthenticatedBudget, cfg.GlobalBudget = 10, 10
	stores := []*store{newStore(db, cfg), newStore(db, cfg)}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	results := make(chan *lease, 2)
	errs := make(chan error, 2)
	for i, candidate := range stores {
		go func(actor string, candidate *store) {
			<-start
			l, err := candidate.AcquireFetch(context.Background(), "1234567890", actor, now)
			results <- l
			errs <- err
		}(string(rune('a'+i)), candidate)
	}
	close(start)
	winners := 0
	for range stores {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if <-results != nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("lease winners = %d, want 1", winners)
	}
}

func TestStoreReadyPostStillHonorsRefetchInterval(t *testing.T) {
	s, now := testStore(t)
	first, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || first == nil {
		t.Fatalf("first lease = %#v, %v", first, err)
	}
	if updated, err := s.CompleteReady(context.Background(), *first, PublicPost{ID: first.PostID, Text: "cached"}, now); err != nil || !updated {
		t.Fatalf("initial cache completion = updated:%v err:%v", updated, err)
	}
	if early, err := s.AcquireFetch(context.Background(), first.PostID, "u2", now.Add(59*time.Minute)); err != nil || early != nil {
		t.Fatalf("early retry = %#v, %v", early, err)
	}
	if due, err := s.AcquireFetch(context.Background(), first.PostID, "u2", now.Add(time.Hour)); err != nil || due == nil {
		t.Fatalf("due retry = %#v, %v", due, err)
	}
}

func TestStoreExpiredPendingLeaseCanRetryBeforeRefetchInterval(t *testing.T) {
	s, now := testStore(t)
	first, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || first == nil {
		t.Fatalf("first lease = %#v, %v", first, err)
	}
	if active, err := s.AcquireFetch(context.Background(), first.PostID, "u2", now.Add(fetchLeaseDuration-time.Second)); err != nil || active != nil {
		t.Fatalf("live pending lease was replaced = %#v, %v", active, err)
	}
	second, err := s.AcquireFetch(context.Background(), first.PostID, "u2", now.Add(fetchLeaseDuration+time.Second))
	if err != nil || second == nil {
		t.Fatalf("expired pending lease did not recover before refetch interval = %#v, %v", second, err)
	}
	if updated, err := s.CompleteReady(context.Background(), *first, PublicPost{ID: first.PostID, Text: "stale"}, now.Add(fetchLeaseDuration+2*time.Second)); err != nil || updated {
		t.Fatalf("stale lease overwrote replacement = updated:%v err:%v", updated, err)
	}
	if updated, err := s.CompleteReady(context.Background(), *second, PublicPost{ID: second.PostID, Text: "fresh"}, now.Add(fetchLeaseDuration+3*time.Second)); err != nil || !updated {
		t.Fatalf("replacement lease could not complete = updated:%v err:%v", updated, err)
	}
}

func TestStoreClaimRejectsExpiredQueueLeaseAndRenewsAcceptedClaim(t *testing.T) {
	cfg := testConfig()
	cfg.RefetchInterval = time.Minute
	cfg.AuthenticatedBudget, cfg.GlobalBudget = 10, 10
	s := newStore(migratedDB(t), cfg)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	queued, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || queued == nil {
		t.Fatalf("queued lease = %#v, %v", queued, err)
	}
	claimAt := now.Add(90 * time.Second)
	claimed, err := s.ClaimFetch(context.Background(), *queued, claimAt)
	if err != nil || claimed == nil {
		t.Fatalf("claim before expiry = %#v, %v", claimed, err)
	}
	if replacement, err := s.AcquireFetch(context.Background(), queued.PostID, "u2", now.Add(121*time.Second)); err != nil || replacement != nil {
		t.Fatalf("replacement during renewed claim = %#v, %v", replacement, err)
	}

	expired, err := s.AcquireFetch(context.Background(), "9999999999", "u1", now)
	if err != nil || expired == nil {
		t.Fatalf("second queued lease = %#v, %v", expired, err)
	}
	if claimed, err := s.ClaimFetch(context.Background(), *expired, now.Add(fetchLeaseDuration)); err != nil || claimed != nil {
		t.Fatalf("expired queue claim = %#v, %v", claimed, err)
	}
}

func TestStoreAuditDeduplicatesUnchangedText(t *testing.T) {
	s, now := testStore(t)
	postID := "1234567890"
	for i, body := range []string{"same", "same", "changed", "same"} {
		at := now.Add(time.Duration(i) * time.Hour)
		l, err := s.AcquireFetch(context.Background(), postID, "u1", at)
		if err != nil || l == nil {
			t.Fatalf("lease %d = %#v, %v", i, l, err)
		}
		_, err = s.CompleteReady(context.Background(), *l, PublicPost{ID: postID, Text: body, Author: PublicAuthor{Name: "A", ScreenName: "a"}}, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListAudit(context.Background(), postID)
	if err != nil || len(items) != 3 {
		t.Fatalf("audit snapshots = %#v, %v", items, err)
	}
}

func TestStoreRefreshClearsStalePayloadAndExpiresPoster(t *testing.T) {
	s, now := testStore(t)
	l, _ := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	post := PublicPost{
		ID: l.PostID, Text: "video", Author: PublicAuthor{Name: "A", ScreenName: "a"},
		Video: &PublicVideo{PosterURL: "https://pbs.twimg.com/poster.jpg", DurationMS: 1000}, HasVideo: true,
	}
	if _, err := s.CompleteReady(context.Background(), *l, post, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), l.PostID)
	if err != nil || entry.Post == nil || entry.Post.Video == nil || entry.Post.Video.PosterExpiresAt == nil ||
		!entry.Post.Video.PosterExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("poster expiry = %#v, %v", entry, err)
	}
	if refreshed, err := s.AcquireFetch(context.Background(), l.PostID, "u1", now.Add(time.Hour)); err != nil || refreshed == nil {
		t.Fatalf("refresh lease = %#v, %v", refreshed, err)
	}
	entry, err = s.ReadPublic(context.Background(), l.PostID)
	if err != nil || entry.State != "pending" || entry.Post != nil {
		t.Fatalf("stale payload survived refresh: %#v, %v", entry, err)
	}
}

func TestStoreUnavailableNeverUsesOrDeletesAudit(t *testing.T) {
	s, now := testStore(t)
	l, _ := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	post := PublicPost{ID: l.PostID, Text: "retained", Author: PublicAuthor{Name: "A", ScreenName: "a"}}
	_, _ = s.CompleteReady(context.Background(), *l, post, now)
	l, err := s.AcquireFetch(context.Background(), l.PostID, "u1", now.Add(time.Hour))
	if err != nil || l == nil {
		t.Fatalf("refresh lease = %#v, %v", l, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *l, "schema", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.ReadPublic(context.Background(), l.PostID)
	if entry.State != "unavailable" || entry.Post != nil || entry.ErrorClass != "schema" {
		t.Fatalf("unavailable entry = %#v", entry)
	}
	items, _ := s.ListAudit(context.Background(), l.PostID)
	if len(items) != 1 || items[0].Body != "retained" {
		t.Fatalf("audit was used or removed: %#v", items)
	}
}

func TestPermanentFailureCannotResurrectOldPublicPayloadOnLaterTransientFailure(t *testing.T) {
	s, now := testStore(t)
	first, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || first == nil {
		t.Fatalf("first lease = %#v, %v", first, err)
	}
	if _, err := s.CompleteReady(context.Background(), *first, PublicPost{ID: first.PostID, Text: "old public text", Author: PublicAuthor{Name: "A", ScreenName: "a"}}, now); err != nil {
		t.Fatal(err)
	}
	permanent, err := s.AcquireFetch(context.Background(), first.PostID, "u1", now.Add(time.Hour))
	if err != nil || permanent == nil {
		t.Fatalf("permanent refresh lease = %#v, %v", permanent, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *permanent, "upstream_not_found", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	transient, err := s.AcquireFetch(context.Background(), first.PostID, "u1", now.Add(2*time.Hour))
	if err != nil || transient == nil {
		t.Fatalf("transient refresh lease = %#v, %v", transient, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *transient, "network", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), first.PostID)
	if err != nil || entry == nil || entry.State != "unavailable" || entry.Post != nil || entry.ErrorClass != "network" {
		t.Fatalf("old payload resurrected after permanent failure: entry=%#v err=%v", entry, err)
	}
}

func TestStoreRateLimitRollsBackLease(t *testing.T) {
	cfg := testConfig()
	cfg.AuthenticatedBudget, cfg.AuthenticatedDailyBudget, cfg.GlobalBudget = 1, 100, 100
	s := newStore(migratedDB(t), cfg)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now); err != nil || l == nil {
		t.Fatalf("first lease = %#v, %v", l, err)
	}
	if l, err := s.AcquireFetch(context.Background(), "9999999999", "u1", now); !errors.Is(err, errRateLimited) || l != nil {
		t.Fatalf("limited lease = %#v, %v", l, err)
	}
	entry, err := s.ReadPublic(context.Background(), "9999999999")
	if err != nil || entry != nil {
		t.Fatalf("rate-limited acquisition committed a row: %#v, %v", entry, err)
	}
}

func TestStoreHourlyAndDailyBudgetsUseUTCBoundaries(t *testing.T) {
	cfg := testConfig()
	cfg.AuthenticatedBudget, cfg.AuthenticatedDailyBudget, cfg.GlobalBudget = 1, 2, 100
	s := newStore(migratedDB(t), cfg)
	firstHour := time.Date(2026, 10, 2, 12, 59, 59, 0, time.FixedZone("JST", 9*60*60))
	if l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", firstHour); err != nil || l == nil {
		t.Fatalf("first lease = %#v, %v", l, err)
	}
	if l, err := s.AcquireFetch(context.Background(), "2234567890", "u1", firstHour.Add(time.Second)); err != nil || l == nil {
		t.Fatalf("next UTC hour lease = %#v, %v", l, err)
	}
	if l, err := s.AcquireFetch(context.Background(), "3234567890", "u1", firstHour.Add(2*time.Second)); !errors.Is(err, errRateLimited) || l != nil {
		t.Fatalf("daily-limited lease = %#v, %v", l, err)
	}
	nextDay := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	if l, err := s.AcquireFetch(context.Background(), "4234567890", "u1", nextDay); err != nil || l == nil {
		t.Fatalf("next UTC day lease = %#v, %v", l, err)
	}
}

func TestStoreTransientFailureUsesLastSuccessButExplicitUnavailableDoesNot(t *testing.T) {
	s, now := testStore(t)
	postID := "1234567890"
	l, _ := s.AcquireFetch(context.Background(), postID, "u1", now)
	post := PublicPost{ID: postID, Text: "last success", Author: PublicAuthor{Name: "A", ScreenName: "a"}}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	l, _ = s.AcquireFetch(context.Background(), postID, "u1", now.Add(time.Hour))
	if _, err := s.CompleteUnavailable(context.Background(), *l, "network", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), postID)
	if err != nil || entry == nil || entry.State != "stale" || !entry.Stale || entry.Post == nil || entry.Post.Text != "last success" {
		t.Fatalf("transient entry = %#v, %v", entry, err)
	}
	l, _ = s.AcquireFetch(context.Background(), postID, "u1", now.Add(2*time.Hour))
	if _, err := s.CompleteUnavailable(context.Background(), *l, "upstream_not_found", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entry, err = s.ReadPublic(context.Background(), postID)
	if err != nil || entry == nil || entry.State != "unavailable" || entry.Stale || entry.Post != nil {
		t.Fatalf("explicit unavailable entry = %#v, %v", entry, err)
	}
}

func TestStoreIndividualDeleteIsIdempotentAndLogged(t *testing.T) {
	s, now := testStore(t)
	l, _ := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	_, _ = s.CompleteReady(context.Background(), *l, PublicPost{ID: l.PostID, Text: "body", Author: PublicAuthor{Name: "A", ScreenName: "a"}}, now)
	for iteration, want := range []int64{2, 0} {
		got, err := s.Delete(context.Background(), "moderator", "both", l.PostID, nil, "request")
		if err != nil || got != want {
			t.Fatalf("delete %d = %d, %v; want %d", iteration, got, err, want)
		}
	}
	var logs int
	if err := s.db.QueryRow(`SELECT count(*) FROM audit_delete_log WHERE actor_id = 'moderator'`).Scan(&logs); err != nil || logs != 2 {
		t.Fatalf("delete logs = %d, %v", logs, err)
	}
}

func TestStoreNormalizesUsersPostsAndQuotesWithoutLosingAuditEvidence(t *testing.T) {
	s, now := testStore(t)
	complete := func(post PublicPost, at time.Time) {
		t.Helper()
		l, err := s.AcquireFetch(context.Background(), post.ID, "u1", at)
		if err != nil || l == nil {
			t.Fatalf("lease for %s = %#v, %v", post.ID, l, err)
		}
		if ok, err := s.CompleteReady(context.Background(), *l, post, at); err != nil || !ok {
			t.Fatalf("complete for %s = %v, %v", post.ID, ok, err)
		}
	}

	shared := PublicAuthor{ID: "111111111", Name: "Alice old", ScreenName: "alice", AvatarURL: "https://pbs.twimg.com/old.jpg"}
	quoteAuthor := PublicAuthor{ID: "222222222", Name: "Bob", ScreenName: "bob"}
	complete(PublicPost{
		ID: "1234567890", Text: "evidence", Author: shared,
		Quote: &PublicQuotedPost{ID: "2234567890", Text: "quoted", Author: quoteAuthor},
	}, now)
	shared.Name = "Alice current"
	shared.ScreenName = "alice_new"
	complete(PublicPost{ID: "3234567890", Text: "second", Author: shared}, now.Add(time.Minute))

	var users, posts int
	if err := s.db.QueryRow(`SELECT count(*) FROM x_user`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM x_post`).Scan(&posts); err != nil {
		t.Fatal(err)
	}
	if users != 2 || posts != 3 {
		t.Fatalf("normalized counts users=%d posts=%d", users, posts)
	}
	var authorID, quoteID, payload string
	if err := s.db.QueryRow(`SELECT author_id, COALESCE(quote_post_id, ''), payload::text FROM x_post WHERE post_id = '1234567890'`).Scan(&authorID, &quoteID, &payload); err != nil {
		t.Fatal(err)
	}
	if authorID != "111111111" || quoteID != "2234567890" || strings.Contains(payload, `"author"`) || strings.Contains(payload, `"quote"`) {
		t.Fatalf("normalized post author=%q quote=%q payload=%s", authorID, quoteID, payload)
	}
	var name, screenName string
	if err := s.db.QueryRow(`SELECT name, screen_name FROM x_user WHERE user_id = '111111111'`).Scan(&name, &screenName); err != nil {
		t.Fatal(err)
	}
	if name != "Alice current" || screenName != "alice_new" {
		t.Fatalf("current profile = %q @%s", name, screenName)
	}
	var snapshot string
	if err := s.db.QueryRow(`SELECT author_snapshot::text FROM audit_body_snapshot WHERE post_id = '1234567890'`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, "Alice old") || strings.Contains(snapshot, "Alice current") {
		t.Fatalf("audit author evidence was rewritten: %s", snapshot)
	}
}

func TestNormalizedPostPayloadStripsCapabilitiesFromLegacyAndVideoArrayMetadata(t *testing.T) {
	payload, err := normalizedPostPayload(map[string]any{
		"id":     "1234567890",
		"video":  map[string]any{"posterUrl": "https://pbs.twimg.com/a.jpg", "embedToken": "legacy-secret", "embedExpiresAt": "2030-01-01T00:00:00Z"},
		"videos": []any{map[string]any{"posterUrl": "https://pbs.twimg.com/a.jpg", "embedToken": "array-secret", "embedExpiresAt": "2030-01-01T00:00:00Z"}},
		"quote":  map[string]any{"video": map[string]any{"embedToken": "quote-secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"legacy-secret", "array-secret", "quote-secret", "embedToken", "embedExpiresAt"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("normalized payload retained capability %q: %s", secret, payload)
		}
	}
}

func TestStoreDoesNotInferStableUserIdentityFromHandle(t *testing.T) {
	s, now := testStore(t)
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	post := PublicPost{ID: l.PostID, Text: "legacy", Author: PublicAuthor{Name: "Alice", ScreenName: "alice"}}
	if ok, err := s.CompleteReady(context.Background(), *l, post, now); err != nil || !ok {
		t.Fatalf("complete = %v, %v", ok, err)
	}
	var users, posts int
	_ = s.db.QueryRow(`SELECT count(*) FROM x_user`).Scan(&users)
	_ = s.db.QueryRow(`SELECT count(*) FROM x_post`).Scan(&posts)
	if users != 0 || posts != 0 {
		t.Fatalf("handle-only identity was normalized: users=%d posts=%d", users, posts)
	}
	entry, err := s.ReadPublic(context.Background(), post.ID)
	if err != nil || entry == nil || entry.Post == nil || entry.Post.Author.ScreenName != "alice" {
		t.Fatalf("legacy payload was not preserved: %#v, %v", entry, err)
	}
}

func seedLegacyCacheRow(t *testing.T, s *store, id, state, payload, lastSuccess string, errorClass any, fetched, successAt time.Time) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO public_post_cache
		(post_id,state,payload,fetched_at,next_fetch_at,last_error_class,last_success_payload,last_success_at)
		VALUES ($1,$2,$3::jsonb,$4,$4,$5,$6::jsonb,$7)`, id, state, nullableJSON(payload), fetched, errorClass, nullableJSON(lastSuccess), nullableTime(successAt))
	if err != nil {
		t.Fatal(err)
	}
}

func nullableJSON(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullableTime(v time.Time) any {
	if v.IsZero() {
		return nil
	}
	return v
}

func TestLegacyUnmarkedNonreadyRowsNeverServeTextAfterTransientRetry(t *testing.T) {
	s, now := testStore(t)
	legacy := `{"id":"1234567890","text":"withdrawn"}`
	seedLegacyCacheRow(t, s, "1234567890", "pending", "", legacy, "upstream_not_found", now, now)
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now.Add(time.Hour))
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *l, "network", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, err := s.ReadPublic(context.Background(), "1234567890")
	if err != nil || e.Post != nil {
		t.Fatalf("unmarked legacy text exposed: %#v %v", e, err)
	}
}

func TestLegacyReadyPayloadIsLazilyPromotedAndPublicPayloadStaysUnmarked(t *testing.T) {
	s, now := testStore(t)
	legacy := `{"id":"1234567890","text":"previously ready"}`
	seedLegacyCacheRow(t, s, "1234567890", "ready", legacy, legacy, nil, now, now)
	var publicPayload string
	if err := s.db.QueryRow(`SELECT payload::text FROM public_post_cache WHERE post_id='1234567890'`).Scan(&publicPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(publicPayload, "_fxTrustedSuccess") {
		t.Fatalf("legacy public payload was marked: %s", publicPayload)
	}
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now.Add(time.Hour))
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *l, "network", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, err := s.ReadPublic(context.Background(), "1234567890")
	if err != nil || e.State != "stale" || e.Post == nil || e.Post.Text != "previously ready" {
		t.Fatalf("promoted legacy cache = %#v %v", e, err)
	}
	var trusted string
	if err := s.db.QueryRow(`SELECT last_success_payload::text FROM public_post_cache WHERE post_id='1234567890'`).Scan(&trusted); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trusted, `"_fxTrustedSuccess": 1`) {
		t.Fatalf("trusted=%s", trusted)
	}
}

func TestNewSuccessTrustsStaleCacheOnlyThroughTransientFailure(t *testing.T) {
	s, now := testStore(t)
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || l == nil {
		t.Fatalf("lease = %#v %v", l, err)
	}
	if _, err := s.CompleteReady(context.Background(), *l, PublicPost{ID: l.PostID, Text: "fresh"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireFetch(context.Background(), l.PostID, "u1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	l2 := lease{PostID: l.PostID}
	if err := s.db.QueryRow(`SELECT lease_token FROM public_post_cache WHERE post_id=$1`, l.PostID).Scan(&l2.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), l2, "network", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, err := s.ReadPublic(context.Background(), l.PostID)
	if err != nil || e.State != "stale" || e.Post == nil || e.Post.Text != "fresh" {
		t.Fatalf("trusted new success = %#v %v", e, err)
	}
}

func TestCancelFetchOnlyRestoresTrustedPriorSuccess(t *testing.T) {
	t.Run("unmarked legacy pending", func(t *testing.T) {
		s, now := testStore(t)
		legacy := `{"id":"1234567890","text":"withdrawn"}`
		seedLegacyCacheRow(t, s, "1234567890", "pending", "", legacy, nil, now, now)
		l := lease{PostID: "1234567890", Token: "queued"}
		if _, err := s.db.Exec(`UPDATE public_post_cache SET lease_token=$1, lease_expires_at=$2 WHERE post_id=$3`, l.Token, now.Add(time.Minute), l.PostID); err != nil {
			t.Fatal(err)
		}
		if err := s.CancelFetch(context.Background(), l, "enqueue", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		e, err := s.ReadPublic(context.Background(), l.PostID)
		if err != nil || e.Post != nil {
			t.Fatalf("unmarked canceled text exposed: %#v %v", e, err)
		}
	})
	t.Run("trusted ready", func(t *testing.T) {
		s, now := testStore(t)
		legacy := `{"id":"1234567890","text":"still valid"}`
		seedLegacyCacheRow(t, s, "1234567890", "ready", legacy, legacy, nil, now, now)
		l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now.Add(time.Hour))
		if err != nil || l == nil {
			t.Fatalf("lease = %#v %v", l, err)
		}
		if err := s.CancelFetch(context.Background(), *l, "enqueue", now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		e, err := s.ReadPublic(context.Background(), l.PostID)
		if err != nil || e.State != "ready" || e.Post == nil || e.Post.Text != "still valid" {
			t.Fatalf("trusted canceled cache = %#v %v", e, err)
		}
		var payload string
		if err := s.db.QueryRow(`SELECT payload::text FROM public_post_cache WHERE post_id=$1`, l.PostID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "_fxTrustedSuccess") {
			t.Fatalf("marker leaked into restored public payload: %s", payload)
		}
	})
}

func TestLegacyReadyWithoutFetchedTimestampIsNotPromoted(t *testing.T) {
	s, now := testStore(t)
	legacy := `{"id":"1234567890","text":"timestamp missing"}`
	seedLegacyCacheRow(t, s, "1234567890", "ready", legacy, legacy, nil, now, now)
	if _, err := s.db.Exec(`UPDATE public_post_cache SET fetched_at=NULL WHERE post_id='1234567890'`); err != nil {
		t.Fatal(err)
	}
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now.Add(time.Hour))
	if err != nil || l == nil {
		t.Fatalf("lease = %#v %v", l, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *l, "network", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, err := s.ReadPublic(context.Background(), "1234567890")
	if err != nil || e.Post != nil {
		t.Fatalf("timestamp-less legacy text exposed: %#v %v", e, err)
	}
}
