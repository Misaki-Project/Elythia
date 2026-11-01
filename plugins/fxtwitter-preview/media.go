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
	"sync"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/imagedecode"
	"golang.org/x/sync/semaphore"
)

const (
	mediaProcessingConcurrency = 1
	mediaRequestCapacity       = 33
	mediaBufferCapacity        = 128 << 20
)

var errMediaBusy = errors.New("media processing is busy")

// 世代ごとに作り直さず、別の利用者・URL・メディア種別も同じ処理枠を使う。
// ヘッダー取得は並行してよいが、画像の読み込み・デコード前には処理枠を取る。
// 受付総数も制限し、待機側に画像バイト列や無制限の通信を積み上げない。
var processMediaAdmission = &mediaAdmission{
	requests: make(chan struct{}, mediaRequestCapacity),
	running:  make(chan struct{}, mediaProcessingConcurrency),
	buffered: semaphore.NewWeighted(mediaBufferCapacity),
}

type mediaAdmission struct {
	requests chan struct{}
	running  chan struct{}
	buffered *semaphore.Weighted
}

func (a *mediaAdmission) admit(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case a.requests <- struct{}{}:
	default:
		return nil, errMediaBusy
	}
	if err := ctx.Err(); err != nil {
		<-a.requests
		return nil, err
	}
	return releaseMediaToken(a.requests), nil
}

func (a *mediaAdmission) acquireProcessing(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case a.running <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-a.running
			return nil, err
		}
		return releaseMediaToken(a.running), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func releaseMediaToken(tokens chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { <-tokens }) }
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
		if index != 0 || entry.Post.Video == nil ||
			(entry.State != "stale" && (entry.Post.Video.PosterExpiresAt == nil || !entry.Post.Video.PosterExpiresAt.After(now))) {
			return "", errors.New("poster unavailable")
		}
		return entry.Post.Video.PosterURL, nil
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
		if index != 0 || entry.Post.Quote == nil || entry.Post.Quote.Video == nil ||
			(entry.State != "stale" && (entry.Post.Quote.Video.PosterExpiresAt == nil || !entry.Post.Quote.Video.PosterExpiresAt.After(now))) {
			return "", errors.New("quoted poster unavailable")
		}
		return entry.Post.Quote.Video.PosterURL, nil
	default:
		return "", errors.New("unknown media kind")
	}
}

func fetchMedia(ctx, responseCtx context.Context, base *http.Client, rawURL string, maxBytes int) (plugin.Blob, error) {
	return fetchMediaWithDecoder(ctx, responseCtx, base, rawURL, maxBytes, imagedecode.DecodeImage)
}

func fetchMediaWithDecoder(ctx, responseCtx context.Context, base *http.Client, rawURL string, maxBytes int, decode func([]byte) (image.Image, error)) (plugin.Blob, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !allowedMediaHost(u.Host) || u.User != nil {
		return plugin.Blob{}, errors.New("media URL is not allowlisted")
	}
	// The host writes Blob.Body after the plugin handler returns. Only the incoming
	// HTTP request context covers that write; the shorter fetch context does not.
	// A host without a request-completion signal cannot safely serve this buffer.
	if responseCtx == nil || responseCtx.Done() == nil {
		return plugin.Blob{}, errors.New("media response lifetime is unavailable")
	}
	if err := responseCtx.Err(); err != nil {
		return plugin.Blob{}, err
	}
	if maxBytes <= 0 || maxBytes > 50<<20 {
		return plugin.Blob{}, errors.New("invalid media byte limit")
	}
	releaseRequest, err := processMediaAdmission.admit(ctx)
	if err != nil {
		return plugin.Blob{}, err
	}
	var releaseBuffer func()
	retained := false
	releaseResponse := func() {
		if releaseBuffer != nil {
			releaseBuffer()
		}
		releaseRequest()
	}
	defer func() {
		if !retained {
			releaseResponse()
		}
	}()
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return plugin.Blob{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return plugin.Blob{}, err
	}
	var releaseProcessing func()
	defer func() {
		_ = res.Body.Close()
		if releaseProcessing != nil {
			releaseProcessing()
		}
	}()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return plugin.Blob{}, fmt.Errorf("media status %d", res.StatusCode)
	}
	declared, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !allowedImageMIME(declared) {
		return plugin.Blob{}, errors.New("media content type is not allowed")
	}
	// Reserve before reading, including spare capacity from io.ReadAll growth.
	// Retain both credits while the host may still reference the response bytes.
	bufferCost := 2*int64(maxBytes) + 1024
	if err := processMediaAdmission.buffered.Acquire(ctx, bufferCost); err != nil {
		return plugin.Blob{}, err
	}
	var bufferOnce sync.Once
	releaseBuffer = func() { bufferOnce.Do(func() { processMediaAdmission.buffered.Release(bufferCost) }) }
	// デコーダーはcontextで中断できない。キャンセル後も完了までは枠を保持する。
	releaseProcessing, err = processMediaAdmission.acquireProcessing(ctx)
	if err != nil {
		return plugin.Blob{}, err
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(maxBytes)+1))
	if err != nil {
		return plugin.Blob{}, err
	}
	if len(body) > maxBytes {
		return plugin.Blob{}, errors.New("media response exceeds configured limit")
	}
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(body))
	if !allowedImageMIME(detected) || detected != declared {
		return plugin.Blob{}, errors.New("media content does not match type")
	}
	if err := ctx.Err(); err != nil {
		return plugin.Blob{}, err
	}
	if _, err := decode(body); err != nil {
		return plugin.Blob{}, fmt.Errorf("decode media: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return plugin.Blob{}, err
	}
	if err := responseCtx.Err(); err != nil {
		return plugin.Blob{}, err
	}
	context.AfterFunc(responseCtx, releaseResponse)
	retained = true
	return plugin.Blob{ContentType: detected, Body: body, CacheControl: "private, no-store"}, nil
}

func allowedMediaHost(value string) bool {
	switch value {
	case "pbs.twimg.com":
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
