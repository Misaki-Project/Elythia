// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFxClientRejectsRedirectWithoutFollowing(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://evil.example/redirect"}, "Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})
	client := newFxClient(&http.Client{Transport: transport}, testConfig())
	if _, err := client.Fetch(context.Background(), "1234567890"); err == nil {
		t.Fatal("redirect accepted")
	}
	if requests != 1 {
		t.Fatalf("redirect caused %d requests", requests)
	}
}

func TestFxClientAppliesConfiguredRequestDeadline(t *testing.T) {
	cfg := testConfig()
	cfg.RequestTimeout = 2 * time.Second
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > cfg.RequestTimeout || time.Until(deadline) < time.Second {
			t.Fatalf("unexpected deadline: %v, %v", deadline, ok)
		}
		return nil, errors.New("stop")
	})
	client := newFxClient(&http.Client{Transport: transport}, cfg)
	if _, err := client.Fetch(context.Background(), "1234567890"); err == nil {
		t.Fatal("transport error accepted")
	}
}

func TestFxClientUsesFixedEndpointAndReturnsSanitizedPost(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.fxtwitter.com" || r.URL.Path != "/2/status/1234567890" {
			t.Fatalf("unexpected upstream URL: %s", r.URL)
		}
		return response(200, "application/json", `{
			"code":200,"status":{"id":"1234567890","text":"hello","created_at":"Fri Oct 02 00:00:00 +0000 2026","created_timestamp":1790899200,
			"likes":1,"reposts":2,"quotes":3,"replies":4,
			"author":{"id":"111111111","name":"Alice","screen_name":"alice","avatar_url":"https://pbs.twimg.com/a.jpg"},
			"media":{"photos":[{"type":"photo","url":"https://pbs.twimg.com/p.jpg","width":640,"height":480,"altText":"alt"}],
			"videos":[{"type":"video","url":"https://video.twimg.com/direct.mp4","thumbnail_url":"https://pbs.twimg.com/v.jpg","duration":1200,
			"formats":[{"url":"https://video.twimg.com/format.mp4"}]}]},
			"quote":{"id":"2234567890","text":"quoted text","created_at":"Fri Oct 02 00:00:00 +0000 2026","created_timestamp":1790899200,
			"likes":5,"reposts":6,"quotes":7,"replies":8,
			"author":{"id":"222222222","name":"Bob","screen_name":"bob","avatar_url":"https://pbs.twimg.com/b.jpg"},
			"media":{"photos":[{"type":"photo","url":"https://pbs.twimg.com/q.jpg","width":320,"height":240,"altText":"quoted alt"}]}}
			}
		}`), nil
	})
	cfg := testConfig()
	cfg.MaxResponseBytes = 2048
	c := newFxClient(&http.Client{Transport: transport}, cfg)
	post, err := c.Fetch(context.Background(), "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.ID != "1234567890" || post.Text != "hello" || len(post.Images) != 1 || !post.HasVideo {
		t.Fatalf("unexpected post: %#v", post)
	}
	if post.Video == nil || post.Video.PosterURL != "https://pbs.twimg.com/v.jpg" {
		t.Fatalf("media.videos[].thumbnail_url was not preserved as the poster: %#v", post.Video)
	}
	if !strings.Contains(mustJSON(t, post.Video), `"provider":"x"`) {
		t.Fatalf("native X video provider missing: %#v", post.Video)
	}
	if post.Quote == nil || post.Quote.ID != "2234567890" || post.Quote.Text != "quoted text" ||
		post.Quote.Author.ScreenName != "bob" || len(post.Quote.Images) != 1 {
		t.Fatalf("quoted post was not preserved: %#v", post.Quote)
	}
	if post.Author.ID != "111111111" || post.Quote.Author.ID != "222222222" {
		t.Fatalf("stable author IDs were not preserved: %#v / %#v", post.Author, post.Quote.Author)
	}
	if post.CreatedAt != "2026-10-02T00:00:00Z" || post.Quote.CreatedAt != "2026-10-02T00:00:00Z" {
		t.Fatalf("post timestamps were not normalized: %q / %q", post.CreatedAt, post.Quote.CreatedAt)
	}
	if strings.Contains(mustJSON(t, post), "direct.mp4") || strings.Contains(mustJSON(t, post), "format.mp4") {
		t.Fatal("direct video URL leaked into public model")
	}
}

func TestFxClientRejectsInvalidInputAndResponse(t *testing.T) {
	called := false
	c := newFxClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return response(200, "application/json", `{}`), nil
	})}, testConfig())
	if _, err := c.Fetch(context.Background(), "abc"); err == nil || called {
		t.Fatal("invalid id reached upstream")
	}

	cases := []struct {
		name string
		res  *http.Response
	}{
		{"non-json", response(200, "text/html", `{}`)},
		{"oversize", response(200, "application/json", strings.Repeat("x", testConfig().MaxResponseBytes+1))},
		{"mismatched-id", response(200, "application/json", `{"code":200,"status":{"id":"9999999999","text":"x","author":{"name":"a","screen_name":"b"}}}`)},
		{"tombstone", response(200, "application/json", `{"code":200,"status":{"tombstone":{"text":"gone"}}}`)},
		{"long-text", response(200, "application/json", `{"code":200,"status":{"id":"1234567890","text":"`+strings.Repeat("x", maxFxTextBytes+1)+`","author":{"name":"a","screen_name":"b"}}}`)},
		{"too-many-media", response(200, "application/json", `{"code":200,"status":{"id":"1234567890","text":"x","author":{"name":"a","screen_name":"b"},"media":{"photos":[{},{},{},{},{}]}}}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			if tc.name == "long-text" {
				cfg.MaxResponseBytes = maxFxTextBytes + 4096
			}
			client := newFxClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return tc.res, nil })}, cfg)
			if _, err := client.Fetch(context.Background(), "1234567890"); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestFxClientDoesNotExposeTombstonedQuote(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"main","author":{"name":"a","screen_name":"b"},"quote":{"type":"tombstone","reason":"deleted"}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Quote != nil {
		t.Fatalf("tombstoned quote was exposed: %#v", post.Quote)
	}
}

func TestFxClientRecognizesExternalVideoCardWithoutExposingItsDirectURL(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"external video","author":{"name":"a","screen_name":"b"},"media":{"external":{"type":"video","url":"https://video.example/watch","thumbnail_url":"https://pbs.twimg.com/card.jpg"}}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if !post.HasVideo || post.Video == nil || post.Video.PosterURL != "https://pbs.twimg.com/card.jpg" {
		t.Fatalf("external video card not recognized: %#v", post.Video)
	}
	encoded := mustJSON(t, post)
	if strings.Contains(encoded, "video.example") {
		t.Fatal("external direct URL leaked into public model")
	}
	if !strings.Contains(encoded, `"provider":"external"`) || strings.Contains(encoded, `"providerVideoId"`) {
		t.Fatalf("non-YouTube external video classification = %s", encoded)
	}
}

func TestFxClientKeepsYouTubeExternalURLsAsPosterFallbackForMainAndQuote(t *testing.T) {
	const videoID = "dQw4w9WgXcQ"
	for _, rawURL := range []string{
		"https://youtube.com/watch?v=" + videoID,
		"https://www.youtube.com/watch?v=" + videoID + "&list=PLignored&t=43",
		"https://m.youtube.com/watch?v=" + videoID + "&si=ignored",
		"https://youtu.be/" + videoID + "?si=ignored",
		"https://www.youtube.com/shorts/" + videoID + "?feature=share",
		"https://www.youtube.com/embed/" + videoID,
	} {
		t.Run(rawURL, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"code":200,"status":{"id":"1234567890","text":"main","author":{"name":"a","screen_name":"b"},"media":{"external":{"type":"video","url":%q,"thumbnail_url":"https://pbs.twimg.com/main.jpg"}},"quote":{"id":"2234567890","text":"quote","author":{"name":"q","screen_name":"quoted"},"media":{"external":{"type":"video","url":%q,"thumbnail_url":"https://pbs.twimg.com/quote.jpg"}}}}}`, rawURL, rawURL))
			post, err := parseFxResponse(body, "1234567890")
			if err != nil {
				t.Fatal(err)
			}
			encoded := mustJSON(t, post)
			if strings.Count(encoded, `"provider":"external"`) != 2 || strings.Contains(encoded, `"providerVideoId"`) {
				t.Fatalf("external main/quote fallback = %s", encoded)
			}
			for _, forbidden := range []string{"list=", "t=43", "si=", "feature=", rawURL} {
				if strings.Contains(encoded, forbidden) {
					t.Fatalf("untrusted YouTube URL metadata %q leaked into %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestFxClientRejectsNonCanonicalOrUnsafeYouTubeExternalURLs(t *testing.T) {
	for _, rawURL := range []string{
		"http://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://user@www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com:444/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com.evil.example/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch?list=PLonly",
		"https://www.youtube.com/embed/dQw4w9WgXcQ/extra",
		"https://www.youtube.com/embed/too-short",
		"https://www.youtube.com/watch?v=too-short",
		"https://youtu.be//dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ/",
		"https://youtu.be/dQw4w9WgXcQ/extra",
		"https://youtu.be/dQw4w9WgXc!",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ/",
		"https://youtube.example/shorts/dQw4w9WgXcQ",
	} {
		t.Run(rawURL, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"code":200,"status":{"id":"1234567890","text":"external","author":{"name":"a","screen_name":"b"},"media":{"external":{"type":"video","url":%q,"thumbnail_url":"https://pbs.twimg.com/card.jpg"}}}}`, rawURL))
			post, err := parseFxResponse(body, "1234567890")
			if err != nil {
				t.Fatal(err)
			}
			encoded := mustJSON(t, post)
			if !strings.Contains(encoded, `"provider":"external"`) || strings.Contains(encoded, `"providerVideoId"`) || strings.Contains(encoded, rawURL) {
				t.Fatalf("unsafe YouTube URL was trusted or exposed: %s", encoded)
			}
		})
	}
}

func TestFxClientAcceptsFractionalVideoDurationSeconds(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"video","author":{"name":"a","screen_name":"b"},"media":{"videos":[{"thumbnail_url":"https://pbs.twimg.com/video.jpg","duration":29.5}]}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if !post.HasVideo || post.Video == nil {
		t.Fatalf("fractional-duration video was not recognized: %#v", post.Video)
	}
	if post.Video.DurationMS != 29_500 {
		t.Fatalf("duration = %d ms, want 29500 ms", post.Video.DurationMS)
	}
}

func TestFxClientPreservesValidatedURLAndUnicodeHashtagFacets(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"fallback","author":{"name":"a","screen_name":"b"},"raw_text":{"text":"See https://t.co/a #日本語","facets":[{"type":"url","indices":[4,18],"original":"https://t.co/a","replacement":"https://example.com/article","display":"example.com/article"},{"type":"hashtag","indices":[19,23],"original":"#日本語","replacement":"#日本語","display":"#日本語"},{"type":"url","indices":[4,18],"original":"https://t.co/a","replacement":"ftp://unsafe.example/file","display":"unsafe"}]}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Text != "See https://example.com/article #日本語" || len(post.TextEntities) != 2 {
		t.Fatalf("validated facets were not preserved: %#v", post)
	}
	if post.TextEntities[0].Kind != "url" || post.TextEntities[0].Href != "https://example.com/article" || post.TextEntities[0].Display != "https://example.com/article" || post.TextEntities[0].Start != 4 || post.TextEntities[0].End != 31 {
		t.Fatalf("expanded URL facet = %#v", post.TextEntities[0])
	}
	if post.TextEntities[1].Kind != "hashtag" || post.TextEntities[1].Tag != "日本語" {
		t.Fatalf("unicode hashtag facet = %#v", post.TextEntities[1])
	}
}

func TestFxClientDecodesVisibleHTMLEntitiesExactlyOnceForMainAndQuote(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"fallback","author":{"name":"Alice","screen_name":"alice"},"raw_text":{"text":"&lt;main&gt; &amp;lt;script&amp;gt; &lt;img src=x onerror=alert(1)&gt; &quot;q&quot; &amp;","facets":[]},"quote":{"id":"2234567890","text":"quote fallback","author":{"name":"Bob","screen_name":"bob"},"raw_text":{"text":"&amp;lt;quoted&amp;gt;","facets":[]}}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Text != `<main> &lt;script&gt; <img src=x onerror=alert(1)> "q" &` {
		t.Fatalf("main text = %q", post.Text)
	}
	if post.Quote == nil || post.Quote.Text != `&lt;quoted&gt;` {
		t.Fatalf("quote text = %#v", post.Quote)
	}
}

func TestFxClientDecodesFallbackTextExactlyOnce(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"&lt;fallback&gt; &amp;lt;literal&amp;gt;","author":{"name":"Alice","screen_name":"alice"}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Text != `<fallback> &lt;literal&gt;` {
		t.Fatalf("fallback text = %q", post.Text)
	}
}

func TestFxClientDecodesFacetOutputAfterRawUTF16Matching(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"fallback","author":{"name":"Alice","screen_name":"alice"},"raw_text":{"text":"😀 https://t.co/a &lt;ok&gt; #tag @bob","facets":[{"type":"url","indices":[3,17],"original":"https://t.co/a","replacement":"https://example.com/a?x=1&amp;y=2&amp;literal=&amp;lt;x&amp;gt;","display":"example.com/a"},{"type":"hashtag","indices":[29,33],"original":"#tag","replacement":"#tag","display":"#tag"},{"type":"mention","indices":[34,38],"original":"@bob","replacement":"@bob","display":"@bob"}]}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	wantText := "😀 https://example.com/a?x=1&y=2&literal=&lt;x&gt; <ok> #tag @bob"
	if post.Text != wantText {
		t.Fatalf("visible text = %q, want %q", post.Text, wantText)
	}
	if len(post.TextEntities) != 3 {
		t.Fatalf("entities = %#v", post.TextEntities)
	}
	for i, want := range []struct {
		kind, display, href, tag string
		prefix, visible          string
	}{
		{kind: "url", display: "https://example.com/a?x=1&y=2&literal=&lt;x&gt;", href: "https://example.com/a?x=1&y=2&literal=&lt;x&gt;", prefix: "😀 ", visible: "https://example.com/a?x=1&y=2&literal=&lt;x&gt;"},
		{kind: "hashtag", display: "#tag", tag: "tag", prefix: "😀 https://example.com/a?x=1&y=2&literal=&lt;x&gt; <ok> ", visible: "#tag"},
		{kind: "mention", display: "@bob", href: "https://x.com/bob", prefix: "😀 https://example.com/a?x=1&y=2&literal=&lt;x&gt; <ok> #tag ", visible: "@bob"},
	} {
		entity := post.TextEntities[i]
		wantStart := len(utf16.Encode([]rune(want.prefix)))
		wantEnd := wantStart + len(utf16.Encode([]rune(want.visible)))
		if entity.Kind != want.kind || entity.Display != want.display || entity.Href != want.href || entity.Tag != want.tag || entity.Start != wantStart || entity.End != wantEnd {
			t.Fatalf("entity %d = %#v, want kind=%q display=%q href=%q tag=%q range=%d:%d", i, entity, want.kind, want.display, want.href, want.tag, wantStart, wantEnd)
		}
	}
}

func TestFxClientBuildsMentionAndAuthorProfileLinksOnlyFromValidHandles(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"fallback","author":{"name":"Alice","screen_name":"alice_ok"},"raw_text":{"text":"Hi @bob and mail@example.com and ＠無効","facets":[{"type":"mention","indices":[3,7],"original":"@bob","replacement":"@bob","display":"@bob"},{"type":"mention","indices":[33,36],"original":"＠無効","replacement":"＠無効","display":"＠無効"}]}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Author.ProfileURL != "https://x.com/alice_ok" {
		t.Fatalf("author profile URL = %q", post.Author.ProfileURL)
	}
	if len(post.TextEntities) != 1 || post.TextEntities[0].Kind != "mention" || post.TextEntities[0].Href != "https://x.com/bob" {
		t.Fatalf("mention entities = %#v", post.TextEntities)
	}
}

func TestFxClientRewritesURLFacetsAndRemovesMediaWithUTF16Offsets(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"fallback","author":{"name":"Alice","screen_name":"alice"},"raw_text":{"text":"😀 https://t.co/a https://t.co/m #日本語 https://t.co/b","facets":[{"type":"url","indices":[3,17],"original":"https://t.co/a","replacement":"https://example.com/one","display":"example.com/one"},{"type":"media","indices":[18,32],"original":"https://t.co/m","replacement":"https://pbs.twimg.com/media.jpg","display":"pic.x.com/media"},{"type":"hashtag","indices":[33,37],"original":"#日本語","replacement":"#日本語","display":"#日本語"},{"type":"url","indices":[38,52],"original":"https://t.co/b","replacement":"https://example.org/two","display":"example.org/two"}]}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Text != "😀 https://example.com/one #日本語 https://example.org/two" {
		t.Fatalf("visible text = %q", post.Text)
	}
	if strings.Contains(post.Text, "t.co") || strings.Contains(post.Text, "pbs.twimg.com") {
		t.Fatalf("short or media URL leaked into visible text: %q", post.Text)
	}
	if len(post.TextEntities) != 3 {
		t.Fatalf("entities = %#v", post.TextEntities)
	}
	for i, want := range []struct {
		kind, href string
		start, end int
	}{
		{"url", "https://example.com/one", 3, 26},
		{"hashtag", "", 27, 31},
		{"url", "https://example.org/two", 32, 55},
	} {
		entity := post.TextEntities[i]
		if entity.Kind != want.kind || entity.Start != want.start || entity.End != want.end || entity.Href != want.href {
			t.Fatalf("entity %d = %#v in %q", i, entity, post.Text)
		}
	}
}

func TestFxClientKeepsUnresolvedShortURLAsPlainText(t *testing.T) {
	for _, replacement := range []string{"javascript:alert(1)", "javascript&#58;alert(1)", "data&#58;text/html,unsafe"} {
		body := []byte(fmt.Sprintf(`{"code":200,"status":{"id":"1234567890","text":"fallback","author":{"name":"Alice","screen_name":"alice"},"raw_text":{"text":"See https://t.co/a","facets":[{"type":"url","indices":[4,18],"original":"https://t.co/a","replacement":%q,"display":"unsafe"}]}}}`, replacement))
		post, err := parseFxResponse(body, "1234567890")
		if err != nil {
			t.Fatal(err)
		}
		if post.Text != "See https://t.co/a" || len(post.TextEntities) != 0 {
			t.Fatalf("unresolved URL %q must remain plain: %#v", replacement, post)
		}
	}
}

func TestFxClientNormalizesQuotedRawTextAndSensitivityIndependently(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"main fallback","possibly_sensitive":false,"author":{"name":"Alice","screen_name":"alice"},"raw_text":{"text":"Main https://t.co/a","facets":[{"type":"url","indices":[5,19],"original":"https://t.co/a","replacement":"https://example.com/main","display":"example.com/main"}]},"quote":{"id":"2234567890","text":"quote fallback","possibly_sensitive":true,"author":{"name":"Bob","screen_name":"bob"},"raw_text":{"text":"Quote https://t.co/m https://t.co/q","facets":[{"type":"media","indices":[6,20],"original":"https://t.co/m","replacement":"https://pbs.twimg.com/q.jpg","display":"pic.x.com/q"},{"type":"url","indices":[21,35],"original":"https://t.co/q","replacement":"https://example.org/quote","display":"example.org/quote"}]}}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Text != "Main https://example.com/main" || post.IsSensitive == nil || *post.IsSensitive {
		t.Fatalf("main normalization = %#v", post)
	}
	if post.Quote == nil || post.Quote.Text != "Quote https://example.org/quote" || post.Quote.IsSensitive == nil || !*post.Quote.IsSensitive {
		t.Fatalf("quote normalization = %#v", post.Quote)
	}
}

func TestFxClientLeavesMissingSensitivityUnknown(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"plain","author":{"name":"Alice","screen_name":"alice"}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.IsSensitive != nil {
		t.Fatalf("missing possibly_sensitive was guessed: %#v", post.IsSensitive)
	}
}
