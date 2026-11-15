// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestBuildEmbedURLContainsOnlyValidatedCapabilityData(t *testing.T) {
	nonce := "0123456789abcdef0123456789abcdef"
	raw, err := buildEmbedURL("1234567890", "dark", "ja", nonce)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "platform.twitter.com" || u.Path != "/embed/index.html" {
		t.Fatalf("unexpected embed URL: %s", raw)
	}
	want := map[string]string{
		"id": "1234567890", "embedId": nonce, "theme": "dark", "lang": "ja",
		"hideCard": "false", "hideThread": "false", "dnt": "true",
	}
	for key, value := range want {
		if got := u.Query().Get(key); got != value {
			t.Fatalf("query %s = %q, want %q", key, got, value)
		}
	}
	for _, tc := range [][4]string{{"bad", "dark", "ja", nonce}, {"1234567890", "system", "ja", nonce}, {"1234567890", "dark", "xx", nonce}, {"1234567890", "dark", "ja", "<script>"}} {
		if _, err := buildEmbedURL(tc[0], tc[1], tc[2], tc[3]); err == nil {
			t.Fatalf("accepted %#v", tc)
		}
	}
}

func TestResolveEmbedRequiresMatchingUnexpiredCacheToken(t *testing.T) {
	s, now := testStore(t)
	l, _ := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	post := PublicPost{ID: l.PostID, Text: "video", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true, Video: &PublicVideo{Provider: "x", PosterURL: "https://pbs.twimg.com/p.jpg"}}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.ReadPublic(context.Background(), l.PostID)
	if entry.Post.Video.EmbedToken == "" {
		t.Fatal("embed token missing")
	}
	if err := s.ResolveEmbed(context.Background(), l.PostID, entry.Post.Video.EmbedToken, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveEmbed(context.Background(), l.PostID, strings.Repeat("0", 32), now); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := s.ResolveEmbed(context.Background(), l.PostID, entry.Post.Video.EmbedToken, now.Add(s.cfg.RefetchInterval)); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestResolveEmbedTargetUsesCachedMainAndQuoteProviderMetadata(t *testing.T) {
	s, now := testStore(t)
	l, _ := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	post := PublicPost{
		ID: "1234567890", Text: "videos", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true,
		Video: &PublicVideo{Provider: "x", PosterURL: "https://pbs.twimg.com/main.jpg"},
		Quote: &PublicQuotedPost{
			ID: "2234567890", Text: "quote", Author: PublicAuthor{Name: "Q", ScreenName: "q"}, HasVideo: true,
			Video: &PublicVideo{Provider: "x", PosterURL: "https://pbs.twimg.com/quote.jpg"},
		},
	}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), l.PostID)
	if err != nil || entry == nil || entry.Post == nil || entry.Post.Video == nil || entry.Post.Quote == nil || entry.Post.Quote.Video == nil {
		t.Fatalf("entry = %#v, err = %v", entry, err)
	}
	token := entry.Post.Video.EmbedToken
	if token == "" || entry.Post.Quote.Video.EmbedToken != token {
		t.Fatalf("main/quote capabilities = %q / %q", token, entry.Post.Quote.Video.EmbedToken)
	}
	main, err := s.ResolveEmbedTarget(context.Background(), post.ID, token, "main", now)
	if err != nil || main.Provider != "x" || main.PostID != post.ID || main.VideoID != "" {
		t.Fatalf("main target = %#v, err = %v", main, err)
	}
	quote, err := s.ResolveEmbedTarget(context.Background(), post.ID, token, "quote", now)
	if err != nil || quote.Provider != "x" || quote.PostID != post.Quote.ID || quote.VideoID != "" {
		t.Fatalf("quote target = %#v, err = %v", quote, err)
	}
	if _, err := s.ResolveEmbedTarget(context.Background(), post.ID, token, "other", now); err == nil {
		t.Fatal("unknown embed target accepted")
	}
}

func TestStoreDoesNotIssueEmbedCapabilityForUnknownExternalVideo(t *testing.T) {
	s, now := testStore(t)
	l, _ := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	post := PublicPost{
		ID: "1234567890", Text: "external", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true,
		Video: &PublicVideo{Provider: "external", PosterURL: "https://pbs.twimg.com/card.jpg"},
	}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), post.ID)
	if err != nil || entry == nil || entry.Post == nil || entry.Post.Video == nil {
		t.Fatalf("entry = %#v, err = %v", entry, err)
	}
	if entry.Post.Video.EmbedToken != "" {
		t.Fatalf("external fallback received embed capability: %#v", entry.Post.Video)
	}
}
