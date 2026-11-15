// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/imagedecode"
	"golang.org/x/sync/semaphore"
)

const (
	mediaProcessingConcurrency = 1
	mediaRequestCapacity       = 16
	mediaBufferCapacity        = 128 << 20
	mediaResponseWriteTimeout  = 2 * time.Minute
)

var errMediaBusy = errors.New("media processing capacity is unavailable")
var errMediaBodyTooLarge = errors.New("media response exceeds configured limit")

var processMediaAdmission = newMediaAdmission(mediaProcessingConcurrency, mediaRequestCapacity, mediaBufferCapacity)

type mediaAdmission struct {
	requests chan struct{}
	decoders chan struct{}
	buffered *semaphore.Weighted
	capacity int64
}

func newMediaAdmission(concurrency, requestCapacity, bufferCapacity int) *mediaAdmission {
	return &mediaAdmission{
		requests: make(chan struct{}, requestCapacity),
		decoders: make(chan struct{}, concurrency),
		buffered: semaphore.NewWeighted(int64(bufferCapacity)),
		capacity: int64(bufferCapacity),
	}
}

func (a *mediaAdmission) admit(ctx context.Context, maxBytes ...int) (func(), error) {
	if len(maxBytes) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case a.requests <- struct{}{}:
			return releaseMediaToken(a.requests), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(maxBytes) != 1 {
		return nil, errMediaBusy
	}
	return a.admitWithQueue(ctx, maxBytes[0], false)
}

func (a *mediaAdmission) admitWithQueue(ctx context.Context, maxBytes int, wait bool) (func(), error) {
	reserveBytes := int64(maxBytes) + 1
	if maxBytes < 1 || reserveBytes > a.capacity {
		return nil, errMediaBusy
	}
	if wait {
		select {
		case a.requests <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		select {
		case a.requests <- struct{}{}:
		default:
			return nil, errMediaBusy
		}
	}
	if err := a.buffered.Acquire(ctx, reserveBytes); err != nil {
		<-a.requests
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			a.buffered.Release(reserveBytes)
			<-a.requests
		})
	}, nil
}

func (a *mediaAdmission) acquireDecoder(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case a.decoders <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-a.decoders
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { <-a.decoders }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *mediaAdmission) acquireProcessing(ctx context.Context) (func(), error) {
	return a.acquireDecoder(ctx)
}

func releaseMediaToken(tokens chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { <-tokens }) }
}

func (s *store) ResolveMedia(ctx context.Context, postID, kind, rawIndex string, now time.Time) (string, error) {
	url, _, err := s.ResolveMediaDescriptor(ctx, postID, kind, rawIndex, now)
	return url, err
}

func (s *store) ResolveMediaDescriptor(ctx context.Context, postID, kind, rawIndex string, now time.Time) (string, *time.Time, error) {
	if !statusIDPattern.MatchString(postID) {
		return "", nil, errors.New("invalid post id")
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		return "", nil, errors.New("invalid media index")
	}
	entry, err := s.ReadPublic(ctx, postID)
	if err != nil {
		return "", nil, err
	}
	if entry == nil || (entry.State != "ready" && entry.State != "stale") || entry.Post == nil || !entry.NextFetchAt.After(now) {
		return "", nil, errors.New("media descriptor unavailable")
	}
	var source string
	switch kind {
	case "avatar":
		if index != 0 {
			return "", nil, errors.New("avatar index unavailable")
		}
		source = entry.Post.Author.AvatarURL
	case "image":
		if index >= len(entry.Post.Images) {
			return "", nil, errors.New("image index unavailable")
		}
		source = entry.Post.Images[index].URL
	case "poster":
		video := indexedVideo(entry.Post.Videos, entry.Post.Video, index)
		if video == nil ||
			(entry.State != "stale" && (video.PosterExpiresAt == nil || !video.PosterExpiresAt.After(now))) {
			return "", nil, errors.New("poster unavailable")
		}
		source = video.PosterURL
	case "quote-avatar":
		if index != 0 || entry.Post.Quote == nil {
			return "", nil, errors.New("quoted avatar unavailable")
		}
		source = entry.Post.Quote.Author.AvatarURL
	case "quote-image":
		if entry.Post.Quote == nil || index >= len(entry.Post.Quote.Images) {
			return "", nil, errors.New("quoted image unavailable")
		}
		source = entry.Post.Quote.Images[index].URL
	case "quote-poster":
		if entry.Post.Quote == nil {
			return "", nil, errors.New("quoted poster unavailable")
		}
		video := indexedVideo(entry.Post.Quote.Videos, entry.Post.Quote.Video, index)
		if video == nil || (entry.State != "stale" && (video.PosterExpiresAt == nil || !video.PosterExpiresAt.After(now))) {
			return "", nil, errors.New("quoted poster unavailable")
		}
		source = video.PosterURL
	default:
		return "", nil, errors.New("unknown media kind")
	}
	return source, entry.FetchedAt, nil
}

func indexedVideo(videos []PublicVideoMetadata, legacy *PublicVideo, index int) *PublicVideoMetadata {
	if index < 0 {
		return nil
	}
	if len(videos) > 0 {
		if index >= len(videos) {
			return nil
		}
		return &videos[index]
	}
	if index != 0 || legacy == nil {
		return nil
	}
	return &PublicVideoMetadata{Provider: legacy.Provider, ProviderVideoID: legacy.ProviderVideoID, PosterURL: legacy.PosterURL, PosterExpiresAt: legacy.PosterExpiresAt, DurationMS: legacy.DurationMS}
}

func fetchMedia(ctx context.Context, base *http.Client, rawURL string, maxBytes int) (plugin.Blob, error) {
	blob, release, err := fetchMediaWithAdmission(ctx, base, rawURL, maxBytes, processMediaAdmission)
	if release != nil {
		release()
	}
	return blob, err
}

// fetchMediaWithDecoder keeps the request-lifetime guard for existing focused
// decoder tests. Production routes use fetchMediaWithAdmission and retain the
// returned reservation until the host finishes writing the response.
func fetchMediaWithDecoder(ctx, responseCtx context.Context, base *http.Client, rawURL string, maxBytes int, decode func([]byte) (image.Image, error)) (plugin.Blob, error) {
	if responseCtx == nil || responseCtx.Done() == nil {
		return plugin.Blob{}, errors.New("media response lifetime is unavailable")
	}
	if err := responseCtx.Err(); err != nil {
		return plugin.Blob{}, err
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !allowedMediaHost(u.Host) || u.User != nil {
		return plugin.Blob{}, errors.New("media URL is not allowlisted")
	}
	release, err := processMediaAdmission.admit(ctx, maxBytes)
	if err != nil {
		return plugin.Blob{}, err
	}
	blob, release, err := fetchMediaWithReservationDecoder(ctx, base, u, maxBytes, processMediaAdmission, release, decode)
	if err != nil {
		return plugin.Blob{}, err
	}
	if err := responseCtx.Err(); err != nil {
		release()
		return plugin.Blob{}, err
	}
	context.AfterFunc(responseCtx, release)
	return blob, nil
}

func fetchMediaWithAdmission(ctx context.Context, base *http.Client, rawURL string, maxBytes int, admission *mediaAdmission) (plugin.Blob, func(), error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !allowedMediaHost(u.Host) || u.User != nil {
		return plugin.Blob{}, nil, errors.New("media URL is not allowlisted")
	}
	return fetchAllowedMedia(ctx, base, u, maxBytes, admission)
}

func fetchPosterWithAdmission(ctx context.Context, base *http.Client, rawURL string, maxBytes int, admission *mediaAdmission) (plugin.Blob, func(), error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || !(allowedMediaHost(u.Host) || allowedYouTubePosterURL(u)) {
		return plugin.Blob{}, nil, errors.New("media URL is not allowlisted")
	}
	return fetchAllowedMedia(ctx, base, u, maxBytes, admission)
}

func fetchAllowedMedia(ctx context.Context, base *http.Client, u *url.URL, maxBytes int, admission *mediaAdmission) (plugin.Blob, func(), error) {
	release, err := admission.admit(ctx, maxBytes)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
	return fetchMediaWithReservation(ctx, base, u, maxBytes, admission, release)
}

func fetchMediaWithReservation(ctx context.Context, base *http.Client, u *url.URL, maxBytes int, admission *mediaAdmission, release func()) (plugin.Blob, func(), error) {
	return fetchMediaWithReservationDecoder(ctx, base, u, maxBytes, admission, release, imagedecode.DecodeImage)
}

func fetchMediaWithReservationDecoder(ctx context.Context, base *http.Client, u *url.URL, maxBytes int, admission *mediaAdmission, release func(), decode func([]byte) (image.Image, error)) (plugin.Blob, func(), error) {
	keepReservation := false
	defer func() {
		if !keepReservation {
			release()
		}
	}()
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return plugin.Blob{}, nil, fmt.Errorf("media status %d", res.StatusCode)
	}
	declared, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !allowedImageMIME(declared) {
		return plugin.Blob{}, nil, errors.New("media content type is not allowed")
	}
	if res.ContentLength > int64(maxBytes) {
		return plugin.Blob{}, nil, errors.New("media response exceeds configured limit")
	}
	body, err := readBoundedMediaBody(res.Body, maxBytes)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return plugin.Blob{}, nil, err
	}
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(body))
	if !allowedImageMIME(detected) || detected != declared {
		return plugin.Blob{}, nil, errors.New("media content does not match type")
	}
	releaseDecoder, err := admission.acquireDecoder(ctx)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
	if _, err := decode(body); err != nil {
		releaseDecoder()
		return plugin.Blob{}, nil, fmt.Errorf("decode media: %w", err)
	}
	releaseDecoder()
	if err := ctx.Err(); err != nil {
		return plugin.Blob{}, nil, err
	}
	keepReservation = true
	return plugin.Blob{ContentType: detected, Body: body, CacheControl: "private, no-store", WriteTimeout: mediaResponseWriteTimeout}, release, nil
}

func readBoundedMediaBody(reader io.Reader, maxBytes int) ([]byte, error) {
	buffer := make([]byte, maxBytes+1)
	n, err := io.ReadFull(reader, buffer)
	if n > maxBytes {
		return nil, errMediaBodyTooLarge
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buffer[:n], nil
}

func allowedMediaHost(value string) bool {
	switch value {
	case "pbs.twimg.com":
		return true
	default:
		return false
	}
}

func allowedYouTubePosterURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Host != "img.youtube.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.EscapedPath() != u.Path {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "vi" || !youtubeVideoIDPattern.MatchString(parts[1]) {
		return false
	}
	switch parts[2] {
	case "default.jpg", "mqdefault.jpg", "hqdefault.jpg", "sddefault.jpg", "maxresdefault.jpg", "0.jpg", "1.jpg", "2.jpg", "3.jpg":
		return true
	default:
		return false
	}
}

func allowedImageMIME(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
