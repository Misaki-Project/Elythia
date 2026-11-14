// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPublishedMediaRegistryDeduplicatesWithoutExtendingExpiry(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	registry := newPublishedMediaRegistry(32, 4, 10*time.Minute, 32, 2)

	first, err := registry.Publish("image/jpeg", []byte("image-bytes"), now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Publish("image/jpeg", []byte("image-bytes"), now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.Token != second.Token || !first.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatalf("republication extended or changed URL: first=%+v second=%+v", first, second)
	}
	if first.Extension != "jpg" || first.ContentType != "image/jpeg" || first.Size != int64(len("image-bytes")) {
		t.Fatalf("unexpected publication metadata: %+v", first)
	}

	entry, release, state := registry.Acquire(first.Token, first.Extension, now.Add(9*time.Minute))
	if state != publishedMediaFound {
		t.Fatalf("entry not found before expiry: %v", state)
	}
	defer release()
	if string(entry.Body) != "image-bytes" || entry.ContentType != "image/jpeg" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if got := residualPublicCacheControl(first.ExpiresAt, now.Add(9*time.Minute)); got != "public, max-age=60, s-maxage=60, must-revalidate, stale-if-error=0, no-transform" {
		t.Fatalf("unexpected residual cache control: %q", got)
	}
}

func TestPublishedMediaRegistryMissExpiryAndExtensionMismatch(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	registry := newPublishedMediaRegistry(32, 4, time.Minute, 32, 2)
	publication, err := registry.Publish("image/png", []byte("png-bytes"), now)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, token, ext string
		at               time.Time
		want             publishedMediaLookupState
	}{
		{name: "unknown token", token: strings.Repeat("0", 32), ext: "png", at: now, want: publishedMediaMissing},
		{name: "wrong extension", token: publication.Token, ext: "jpg", at: now, want: publishedMediaMissing},
		{name: "expired", token: publication.Token, ext: "png", at: publication.ExpiresAt, want: publishedMediaMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, release, state := registry.Acquire(tc.token, tc.ext, tc.at)
			if release != nil {
				release()
			}
			if state != tc.want {
				t.Fatalf("state=%v want=%v", state, tc.want)
			}
		})
	}
	if got := residualPublicCacheControl(publication.ExpiresAt, publication.ExpiresAt.Add(-500*time.Millisecond)); got != "" {
		t.Fatalf("subsecond expiry should not be cacheable, got %q", got)
	}
}

func TestPublishedMediaRegistryBoundsBytesEntriesAndActiveReaders(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	registry := newPublishedMediaRegistry(10, 2, time.Minute, 8, 1)
	one, err := registry.Publish("image/png", []byte("12345678"), now)
	if err != nil {
		t.Fatal(err)
	}
	_, release, state := registry.Acquire(one.Token, one.Extension, now)
	if state != publishedMediaFound {
		t.Fatalf("entry not found: %v", state)
	}
	if _, _, state := registry.Acquire(one.Token, one.Extension, now); state != publishedMediaBusy {
		t.Fatalf("second active reader should be bounded, got %v", state)
	}
	if _, err := registry.Publish("image/png", []byte("more-than-eight"), now); !errors.Is(err, errPublishedMediaTooLarge) {
		t.Fatalf("oversized object error=%v", err)
	}
	if _, err := registry.Publish("image/png", []byte("12345"), now); !errors.Is(err, errPublishedMediaFull) {
		t.Fatalf("pinned entry should prevent over-cap admission, error=%v", err)
	}
	release()
	if _, err := registry.Publish("image/png", []byte("123456789"), now); !errors.Is(err, errPublishedMediaTooLarge) {
		t.Fatalf("single object cap error=%v", err)
	}
	second, err := registry.Publish("image/png", []byte("abcdef"), now)
	if err != nil {
		t.Fatal(err)
	}
	third, err := registry.Publish("image/png", []byte("ghijkl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token == third.Token {
		t.Fatal("distinct contents reused a token")
	}
	if got := registry.Stats(); got.Entries > 2 || got.Bytes > 10 {
		t.Fatalf("registry exceeded bounds: %+v", got)
	}
}

func TestPublishedMediaRegistryBoundsSourceAliases(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	registry := newPublishedMediaRegistry(32, 2, time.Minute, 32, 2)
	for i := range 20 {
		key := publishedMediaSourceKey("post", string(rune('a'+i)))
		if _, err := registry.PublishForSource("image/png", []byte("same content"), key, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	stats := registry.Stats()
	if stats.Entries != 1 || stats.SourceAliases > 8 || stats.Bytes != int64(len("same content")) {
		t.Fatalf("source aliases exceeded bounded registry bookkeeping: %+v", stats)
	}
}

func TestPublishedMediaRegistryReusesValidatedSourceAndRefreshesAfterExpiry(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	registry := newPublishedMediaRegistry(32, 4, time.Minute, 32, 2)
	source := publishedMediaSourceKey("post-1", "https://pbs.twimg.com/media/a.jpg", "2026-11-13T12:00:00Z", "1048576")
	first, err := registry.PublishForSource("image/jpeg", []byte("same bytes"), source, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := registry.FindBySource(source, now.Add(30*time.Second)); !ok || got.Token != first.Token || !got.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("source lookup did not reuse live registration: got=%+v ok=%t", got, ok)
	}
	second, err := registry.PublishForSource("image/jpeg", []byte("different bytes"), source, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token || !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("source reuse extended expiry or replaced token: first=%+v second=%+v", first, second)
	}
	third, err := registry.PublishForSource("image/jpeg", []byte("same bytes"), source, first.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if third.Token == first.Token || !third.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("expired registration was not replaced with a new lifetime: first=%+v third=%+v", first, third)
	}
	fourth, err := registry.PublishForSource("image/jpeg", []byte("same bytes"), source, third.ExpiresAt.Add(-1500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Token != third.Token {
		t.Fatal("publication unexpectedly extended an existing unexpired token")
	}
	fifth, err := registry.PublishForSource("image/jpeg", []byte("same bytes"), source, third.ExpiresAt.Add(-500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if fifth.Token == fourth.Token || !fifth.ExpiresAt.After(fourth.ExpiresAt) {
		t.Fatalf("subsecond residual reused a token that immediately misses: current=%+v next=%+v", fourth, fifth)
	}
}

func TestPublishedMediaExtensionAllowlist(t *testing.T) {
	for mimeType, want := range map[string]string{
		"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif",
		"image/webp": "webp", "image/avif": "avif", "video/mp4": "mp4",
	} {
		got, ok := publishedMediaExtension(mimeType)
		if !ok || got != want {
			t.Errorf("publishedMediaExtension(%q)=(%q,%t), want %q", mimeType, got, ok, want)
		}
	}
	for _, mimeType := range []string{"image/svg+xml", "text/html", "application/octet-stream", "image/jpeg; charset=binary"} {
		if got, ok := publishedMediaExtension(mimeType); ok {
			t.Errorf("unsafe/noncanonical MIME %q accepted as %q", mimeType, got)
		}
	}
}

func TestPublishedMediaRegistryRejectsEmptyBody(t *testing.T) {
	registry := newPublishedMediaRegistry(32, 4, time.Minute, 32, 2)
	if _, err := registry.Publish("image/png", nil, time.Now().UTC()); !errors.Is(err, errPublishedMediaEmpty) {
		t.Fatalf("empty media body error=%v", err)
	}
}
