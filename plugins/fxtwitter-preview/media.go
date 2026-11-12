// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/imagedecode"
	"golang.org/x/sync/semaphore"
)

const (
	mediaProcessingConcurrency = 2
	mediaRequestCapacity       = 16
	mediaBufferCapacity        = 128 << 20
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

func (a *mediaAdmission) admit(ctx context.Context, maxBytes int) (func(), error) {
	reserveBytes := int64(maxBytes) + 1
	if maxBytes < 1 || reserveBytes > a.capacity {
		return nil, errMediaBusy
	}
	select {
	case a.requests <- struct{}{}:
	default:
		return nil, errMediaBusy
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
	select {
	case a.decoders <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-a.decoders }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *store) ResolveMedia(ctx context.Context, postID, kind, rawIndex string, now time.Time) (string, error) {
	if !statusIDPattern.MatchString(postID) {
		return "", errors.New("invalid post id")
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		return "", errors.New("invalid media index")
	}
	entry, err := s.ReadPublic(ctx, postID)
	if err != nil {
		return "", err
	}
	if entry == nil || (entry.State != "ready" && entry.State != "stale") || entry.Post == nil || !entry.NextFetchAt.After(now) {
		return "", errors.New("media descriptor unavailable")
	}
	switch kind {
	case "avatar":
		if index != 0 {
			return "", errors.New("avatar index unavailable")
		}
		return entry.Post.Author.AvatarURL, nil
	case "image":
		if index >= len(entry.Post.Images) {
			return "", errors.New("image index unavailable")
		}
		return entry.Post.Images[index].URL, nil
	case "poster":
		video := indexedVideo(entry.Post.Videos, entry.Post.Video, index)
		if video == nil ||
			(entry.State != "stale" && (video.PosterExpiresAt == nil || !video.PosterExpiresAt.After(now))) {
			return "", errors.New("poster unavailable")
		}
		return video.PosterURL, nil
	case "quote-avatar":
		if index != 0 || entry.Post.Quote == nil {
			return "", errors.New("quoted avatar unavailable")
		}
		return entry.Post.Quote.Author.AvatarURL, nil
	case "quote-image":
		if entry.Post.Quote == nil || index >= len(entry.Post.Quote.Images) {
			return "", errors.New("quoted image unavailable")
		}
		return entry.Post.Quote.Images[index].URL, nil
	case "quote-poster":
		if entry.Post.Quote == nil {
			return "", errors.New("quoted poster unavailable")
		}
		video := indexedVideo(entry.Post.Quote.Videos, entry.Post.Quote.Video, index)
		if video == nil || (entry.State != "stale" && (video.PosterExpiresAt == nil || !video.PosterExpiresAt.After(now))) {
			return "", errors.New("quoted poster unavailable")
		}
		return video.PosterURL, nil
	default:
		return "", errors.New("unknown media kind")
	}
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

func fetchMediaWithAdmission(ctx context.Context, base *http.Client, rawURL string, maxBytes int, admission *mediaAdmission) (plugin.Blob, func(), error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !allowedMediaHost(u.Host) || u.User != nil {
		return plugin.Blob{}, nil, errors.New("media URL is not allowlisted")
	}
	release, err := admission.admit(ctx, maxBytes)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
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
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(body))
	if !allowedImageMIME(detected) || detected != declared {
		return plugin.Blob{}, nil, errors.New("media content does not match type")
	}
	releaseDecoder, err := admission.acquireDecoder(ctx)
	if err != nil {
		return plugin.Blob{}, nil, err
	}
	if _, err := imagedecode.DecodeImage(body); err != nil {
		releaseDecoder()
		return plugin.Blob{}, nil, fmt.Errorf("decode media: %w", err)
	}
	releaseDecoder()
	keepReservation = true
	return plugin.Blob{ContentType: detected, Body: body, CacheControl: "private, no-store"}, release, nil
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
	case "pbs.twimg.com", "img.youtube.com":
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
