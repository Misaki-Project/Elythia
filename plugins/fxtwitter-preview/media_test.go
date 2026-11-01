// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"bytes"
	"context"
	"encoding/base64"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"
	"time"
)

func mediaCache(t *testing.T, s *store, now time.Time, imageURL string) {
	t.Helper()
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	post := PublicPost{
		ID: "1234567890", Text: "media", Author: PublicAuthor{Name: "A", ScreenName: "a", AvatarURL: imageURL},
		Images:   []PublicImage{{URL: imageURL, Width: 2, Height: 2}},
		HasVideo: true, Video: &PublicVideo{PosterURL: imageURL},
		Quote: &PublicQuotedPost{
			ID: "2234567890", Text: "quote", Author: PublicAuthor{Name: "Q", ScreenName: "q", AvatarURL: imageURL},
			Images: []PublicImage{{URL: imageURL}}, HasVideo: true, Video: &PublicVideo{PosterURL: imageURL},
		},
	}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
}

func tinyImage(t *testing.T, kind string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var out bytes.Buffer
	var err error
	switch kind {
	case "png":
		err = png.Encode(&out, img)
	case "jpeg":
		err = jpeg.Encode(&out, img, nil)
	case "gif":
		err = gif.Encode(&out, img, nil)
	case "webp":
		data, decodeErr := base64.StdEncoding.DecodeString("UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoCAAIAAgA0JaQAA3AA/vuUAAA=")
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return data
	default:
		t.Fatalf("unknown fixture %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func oversizedPNGHeader(width, height uint32) []byte {
	out := append([]byte(nil), []byte("\x89PNG\r\n\x1a\n")...)
	data := make([]byte, 13)
	data[0], data[1], data[2], data[3] = byte(width>>24), byte(width>>16), byte(width>>8), byte(width)
	data[4], data[5], data[6], data[7] = byte(height>>24), byte(height>>16), byte(height>>8), byte(height)
	data[8], data[9], data[10], data[11], data[12] = 8, 6, 0, 0, 0
	chunk := []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R'}
	chunk = append(chunk, data...)
	crc := crc32.ChecksumIEEE(chunk[4:])
	chunk = append(chunk, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	return append(out, chunk...)
}

func TestMediaFetchAcceptsBoundedRasterFormats(t *testing.T) {
	for _, kind := range []string{"png", "jpeg", "gif", "webp"} {
		t.Run(kind, func(t *testing.T) {
			body := tinyImage(t, kind)
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://pbs.twimg.com/media/test."+kind {
					t.Fatalf("URL = %s", r.URL)
				}
				return response(200, "image/"+kind, string(body)), nil
			})}
			blob, err := fetchTestMedia(context.Background(), client, "https://pbs.twimg.com/media/test."+kind, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if len(blob.Body) != len(body) || blob.CacheControl != "private, no-store" {
				t.Fatalf("blob = %#v", blob)
			}
		})
	}
}

func TestMediaFetchRejectsLegacyYouTubePosterHostBeforeOutbound(t *testing.T) {
	body := tinyImage(t, "jpeg")
	rawURL := "https://img.youtube.com/vi/abc123/hqdefault.jpg"
	called := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.String() != rawURL {
			t.Fatalf("URL = %s", r.URL)
		}
		return response(200, "image/jpeg", string(body)), nil
	})}
	_, err := fetchTestMedia(context.Background(), client, rawURL, 1<<20)
	if err == nil || called {
		t.Fatalf("legacy host allowed: called = %v, err = %v", called, err)
	}
}

func TestMediaFetchRejectsUnsafeOrInvalidResponses(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	for _, raw := range []string{
		"http://pbs.twimg.com/a.png",
		"https://evil.example/a.png",
		"https://pbs.twimg.com.evil/a.png",
		"https://pbs.twimg.com:443/a.png",
		"https://img.youtube.com.evil/a.png",
		"https://img.youtube.com:443/a.png",
	} {
		called = false
		if _, err := fetchTestMedia(context.Background(), client, raw, 1<<20); err == nil || called {
			t.Fatalf("accepted %q", raw)
		}
	}
	for name, res := range map[string]*http.Response{
		"redirect":   {StatusCode: 302, Header: http.Header{"Location": []string{"https://pbs.twimg.com/b.png"}}, Body: http.NoBody},
		"oversize":   response(200, "image/png", string(make([]byte, 1025))),
		"mime":       response(200, "text/html", "<html>"),
		"pixel-bomb": response(200, "image/png", string(oversizedPNGHeader(100000, 100000))),
	} {
		t.Run(name, func(t *testing.T) {
			c := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return res, nil })}
			if _, err := fetchTestMedia(context.Background(), c, "https://pbs.twimg.com/a.png", 1024); err == nil {
				t.Fatal("invalid media accepted")
			}
		})
	}
}

func TestResolveMediaRequiresReadyUnexpiredPublicDescriptor(t *testing.T) {
	s, now := testStore(t)
	mediaCache(t, s, now, "https://pbs.twimg.com/media/test.png")
	for _, tc := range []struct {
		kind, index string
		ok          bool
	}{{"avatar", "0", true}, {"image", "0", true}, {"poster", "0", true},
		{"quote-avatar", "0", true}, {"quote-image", "0", true}, {"quote-poster", "0", true},
		{"image", "1", false}, {"quote-image", "1", false}, {"video", "0", false}} {
		url, err := s.ResolveMedia(context.Background(), "1234567890", tc.kind, tc.index, now.Add(time.Minute))
		if (err == nil) != tc.ok || (tc.ok && url == "") {
			t.Fatalf("%s/%s = %q, %v", tc.kind, tc.index, url, err)
		}
	}
	if _, err := s.ResolveMedia(context.Background(), "1234567890", "image", "0", now.Add(time.Hour)); err == nil {
		t.Fatal("expired descriptor accepted")
	}
}
