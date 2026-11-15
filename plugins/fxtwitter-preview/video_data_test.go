// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseFxResponseKeepsOrderedVideoPostersAndPrivateSources(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"main","author":{"name":"a","screen_name":"b"},"media":{"videos":[{"thumbnail_url":"https://pbs.twimg.com/0.jpg","duration":8,"formats":[{"url":"https://video.twimg.com/a/clip-0.mp4?tag=14","container":"mp4","codec":"h264","bitrate":632000}]},{"thumbnail_url":"https://pbs.twimg.com/1.jpg","duration":9,"formats":[{"url":"https://video.twimg.com/a/clip-1.mp4","container":"mp4","codec":"h264","bitrate":950000}]}]},"quote":{"id":"2234567890","text":"quote","author":{"name":"q","screen_name":"c"},"media":{"videos":[{"thumbnail_url":"https://pbs.twimg.com/q.jpg","duration":7,"formats":[{"url":"https://video.twimg.com/q/clip.mp4","container":"mp4","codec":"h264","bitrate":500000}]}]}}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if !post.HasVideo || len(post.Videos) != 2 || post.Video == nil || post.Video.PosterURL != "https://pbs.twimg.com/0.jpg" {
		t.Fatalf("main video metadata = %#v", post)
	}
	if post.Videos[0].PosterURL != "https://pbs.twimg.com/0.jpg" || post.Videos[1].PosterURL != "https://pbs.twimg.com/1.jpg" {
		t.Fatalf("main video order changed: %#v", post.Videos)
	}
	if len(post.privateVideoSources.Main) != 2 || !strings.Contains(post.privateVideoSources.Main[0].URL, "clip-0.mp4") || !strings.Contains(post.privateVideoSources.Main[1].URL, "clip-1.mp4") {
		t.Fatalf("main source slots changed: %#v", post.privateVideoSources.Main)
	}
	if post.Quote == nil || len(post.Quote.Videos) != 1 || len(post.privateVideoSources.Quote) != 1 || !strings.Contains(post.privateVideoSources.Quote[0].URL, "q/clip.mp4") {
		t.Fatalf("quote video metadata/source missing: quote=%#v sources=%#v", post.Quote, post.privateVideoSources.Quote)
	}
	raw, err := json.Marshal(post)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"video.twimg.com", "_fxVideoSources", "clip-0.mp4"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("private source leaked as public DTO (%q): %s", private, raw)
		}
	}
}

func TestNativeVideoPlaybackURLsArePerItemSameOriginCapabilities(t *testing.T) {
	entry := &CacheEntry{
		State: "ready", PlaybackToken: "0123456789abcdef0123456789abcdef",
		Post: &PublicPost{
			ID: "1234567890", Videos: []PublicVideoMetadata{
				{Provider: "x", PlaybackAvailable: true},
				{Provider: "x", PlaybackAvailable: true},
				{Provider: "youtube", PlaybackAvailable: true},
			},
			Quote: &PublicQuotedPost{ID: "2234567890", Videos: []PublicVideoMetadata{{Provider: "x", PlaybackAvailable: true}}},
		},
	}
	got := publicEntryForMode(entry, "note/with space", "nativeProxy")
	if got.Post.Videos[0].PlaybackURL != videoPlaybackPath("1234567890", "main", 0, "note/with space", entry.PlaybackToken) || got.Post.Videos[1].PlaybackURL != videoPlaybackPath("1234567890", "main", 1, "note/with space", entry.PlaybackToken) {
		t.Fatalf("main playback URLs are not index-specific: %#v", got.Post.Videos)
	}
	if got.Post.Videos[2].PlaybackURL != "" || got.Post.Quote.Videos[0].PlaybackURL != videoPlaybackPath("1234567890", "quote", 0, "note/with space", entry.PlaybackToken) {
		t.Fatalf("provider/quote playback URLs are wrong: main=%#v quote=%#v", got.Post.Videos, got.Post.Quote.Videos)
	}
	for _, video := range append(append([]PublicVideoMetadata(nil), got.Post.Videos...), got.Post.Quote.Videos...) {
		if strings.Contains(video.PlaybackURL, "video.twimg.com") {
			t.Fatalf("external source leaked into playback URL: %q", video.PlaybackURL)
		}
	}
}
