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
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/imagedecode"
)

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

func fetchMedia(ctx context.Context, base *http.Client, rawURL string, maxBytes int) (plugin.Blob, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !allowedMediaHost(u.Host) || u.User != nil {
		return plugin.Blob{}, errors.New("media URL is not allowlisted")
	}
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
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return plugin.Blob{}, fmt.Errorf("media status %d", res.StatusCode)
	}
	declared, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !allowedImageMIME(declared) {
		return plugin.Blob{}, errors.New("media content type is not allowed")
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
	if _, err := imagedecode.DecodeImage(body); err != nil {
		return plugin.Blob{}, fmt.Errorf("decode media: %w", err)
	}
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
