// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

type publishedMediaPrepareRequest struct {
	AuthToken   string `json:"i,omitempty"`
	NoteID      string `json:"noteId"`
	PostID      string `json:"postId"`
	Kind        string `json:"kind"`
	Index       int    `json:"index"`
	Target      string `json:"target,omitempty"`
	PlaybackURL string `json:"playbackUrl,omitempty"`
}

func registerPublishedMediaRoutes(ctx plugin.Context, r plugin.Router, s *store, cfg config) {
	r.POST("/media/prepare", func(req plugin.Request) (any, error) {
		if req.UserID() == "" {
			return nil, plugin.NewCodedStatusError(http.StatusUnauthorized, "login required", "LOGIN_REQUIRED")
		}
		if !cfg.ExternalFetchEnabled {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		if req.Context() == nil || req.Context().Done() == nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		var body publishedMediaPrepareRequest
		if err := req.BindStrict(&body); err != nil || body.NoteID == "" || !statusIDPattern.MatchString(body.PostID) || body.Index < 0 {
			return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "invalid media prepare request", "INVALID_REQUEST")
		}
		maxIndex := maxFxPhotoItems
		if body.Kind == "video" {
			maxIndex = maxFxVideoItems
		}
		if body.Index >= maxIndex {
			return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "invalid media prepare request", "INVALID_REQUEST")
		}
		allowed, _, policyErr := checkedPreviewPolicy(req)
		if policyErr != nil {
			return nil, plugin.NewCodedStatusError(http.StatusInternalServerError, "preview policy resolution failed", "POLICY_UNAVAILABLE")
		}
		if !allowed {
			return nil, plugin.NewCodedStatusError(http.StatusForbidden, "Fx preview is not permitted", "PREVIEW_FORBIDDEN")
		}
		if verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), body.NoteID, body.PostID) != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}

		now := time.Now().UTC()
		if body.Kind == "video" {
			return preparePublishedVideo(req, ctx, s, cfg, body, now)
		}
		if !isPublishedStaticKind(body.Kind) {
			return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "invalid media prepare request", "INVALID_REQUEST")
		}
		if !processMediaRequestLimiter.Allow(req.UserID(), now) {
			return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "media request rate limit exceeded; try again in a minute", "MEDIA_RATE_LIMITED")
		}
		_, rawURL, fetchedAt, err := resolvePublishedStaticSource(req.Context(), s, body, now)
		if err != nil {
			if errors.Is(err, errPublishedMediaPrivateOnly) {
				return nil, plugin.NewCodedStatusError(http.StatusConflict, "media must use the authenticated private route", "PRIVATE_MEDIA_ONLY")
			}
			return nil, plugin.ErrNotFound("media unavailable")
		}
		sourceKey := publishedMediaSourceKey("image-v1", body.PostID, body.Kind, strconv.Itoa(body.Index), rawURL, fetchedAt.UTC().Format(time.RFC3339Nano), strconv.Itoa(cfg.StaticImageMaxBytes))
		if media, ok := processPublishedMediaRegistry.FindBySource(sourceKey, now); ok {
			return publishedMediaPrepareResult(media), nil
		}
		waitCtx, cancelWait := context.WithTimeout(req.Context(), mediaQueueWaitBudget(mediaRequestCapacity, mediaBufferCapacity, mediaUserActiveLimit, cfg.StaticImageMaxBytes, cfg.RequestTimeout, mediaQueueWaitTimeout))
		defer cancelWait()
		releaseUser, err := processMediaRequestGate.acquire(waitCtx, req.UserID(), cfg.StaticImageMaxBytes)
		if err != nil {
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "media request capacity is busy; retry shortly", "MEDIA_BUSY")
		}
		defer releaseUser()
		if waitCtx.Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(waitCtx, ctx.API(), req.UserID(), body.NoteID, body.PostID) != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		currentEntry, currentURL, currentFetchedAt, err := resolvePublishedStaticSource(waitCtx, s, body, time.Now().UTC())
		if err != nil || currentEntry == nil || currentURL != rawURL || !currentFetchedAt.Equal(fetchedAt) {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		fetchCtx, cancelFetch := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancelFetch()
		blob, releaseMedia, err := fetchMediaWithAdmission(fetchCtx, ctx.HTTP(), rawURL, cfg.StaticImageMaxBytes, processMediaAdmission)
		if err != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		defer releaseMedia()
		if req.Context().Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), body.NoteID, body.PostID) != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		finalEntry, finalURL, finalFetchedAt, err := resolvePublishedStaticSource(req.Context(), s, body, time.Now().UTC())
		if err != nil || finalEntry == nil || finalURL != rawURL || !finalFetchedAt.Equal(fetchedAt) {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		media, err := processPublishedMediaRegistry.PublishForSource(blob.ContentType, blob.Body, sourceKey, time.Now().UTC())
		if err != nil {
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "public media capacity is unavailable", "PUBLIC_MEDIA_BUSY")
		}
		return publishedMediaPrepareResult(media), nil
	})

	r.GET("/public-media/:filename", func(req plugin.Request) (any, error) {
		token, extension, ok := parsePublishedMediaFilename(req.Param("filename"))
		if !cfg.ExternalFetchEnabled || !ok || req.Context() == nil || req.Context().Done() == nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		media, release, state := processPublishedMediaRegistry.Acquire(token, extension, time.Now().UTC())
		if state == publishedMediaBusy {
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "public media capacity is busy", "PUBLIC_MEDIA_BUSY")
		}
		if state != publishedMediaFound {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		cacheControl := residualPublicCacheControl(media.ExpiresAt, time.Now().UTC())
		if cacheControl == "" {
			release()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		releaseMediaWhenRequestEnds(req.Context(), release)
		return plugin.Blob{
			ContentType: media.ContentType, Body: media.Body,
			CacheControl: cacheControl, WriteTimeout: mediaResponseWriteTimeout,
		}, nil
	})
}

func isPublishedStaticKind(kind string) bool {
	switch kind {
	case "avatar", "image", "poster", "quote-avatar", "quote-image", "quote-poster":
		return true
	default:
		return false
	}
}

func resolvePublishedStaticSource(ctx context.Context, s *store, body publishedMediaPrepareRequest, now time.Time) (*CacheEntry, string, time.Time, error) {
	entry, err := s.ReadPublic(ctx, body.PostID)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if entry == nil || entry.State != "ready" || entry.Post == nil || entry.FetchedAt == nil || !entry.NextFetchAt.After(now) {
		return nil, "", time.Time{}, errors.New("public media descriptor is unavailable")
	}
	rawURL, fetchedAt, err := s.ResolveMediaDescriptor(ctx, body.PostID, body.Kind, strconv.Itoa(body.Index), now)
	if err != nil || fetchedAt == nil || !fetchedAt.Equal(*entry.FetchedAt) {
		return nil, "", time.Time{}, errors.New("public X media descriptor is unavailable")
	}
	if isPrivateYouTubePoster(entry.Post, body.Kind, body.Index, rawURL) {
		return nil, "", time.Time{}, errPublishedMediaPrivateOnly
	}
	if !publicXDescriptor(entry.Post, body.Kind, body.Index, rawURL) {
		return nil, "", time.Time{}, errors.New("public X media descriptor is unavailable")
	}
	u, parseErr := url.Parse(rawURL)
	if parseErr != nil || u.Scheme != "https" || u.User != nil || !allowedMediaHost(u.Host) {
		return nil, "", time.Time{}, errors.New("public X media source is not allowlisted")
	}
	return entry, rawURL, *fetchedAt, nil
}

var errPublishedMediaPrivateOnly = errors.New("validated private media cannot be published")

func isPrivateYouTubePoster(post *PublicPost, kind string, index int, rawURL string) bool {
	if post == nil || (kind != "poster" && kind != "quote-poster") {
		return false
	}
	video := indexedVideo(post.Videos, post.Video, index)
	if kind == "quote-poster" {
		if post.Quote == nil {
			return false
		}
		video = indexedVideo(post.Quote.Videos, post.Quote.Video, index)
	}
	if video == nil || video.PosterURL != rawURL || (video.Provider != "external" && video.Provider != "youtube") {
		return false
	}
	u, err := url.Parse(rawURL)
	return err == nil && allowedYouTubePosterURL(u)
}

func publicXDescriptor(post *PublicPost, kind string, index int, rawURL string) bool {
	if post == nil || index < 0 || rawURL == "" {
		return false
	}
	switch kind {
	case "avatar":
		return index == 0 && post.Author.AvatarURL == rawURL
	case "image":
		return index < len(post.Images) && post.Images[index].URL == rawURL
	case "poster":
		video := indexedVideo(post.Videos, post.Video, index)
		return video != nil && video.Provider == "x" && video.PosterURL == rawURL
	case "quote-avatar":
		return index == 0 && post.Quote != nil && post.Quote.Author.AvatarURL == rawURL
	case "quote-image":
		return post.Quote != nil && index < len(post.Quote.Images) && post.Quote.Images[index].URL == rawURL
	case "quote-poster":
		if post.Quote == nil {
			return false
		}
		video := indexedVideo(post.Quote.Videos, post.Quote.Video, index)
		return video != nil && video.Provider == "x" && video.PosterURL == rawURL
	default:
		return false
	}
}

func preparePublishedVideo(req plugin.Request, ctx plugin.Context, s *store, cfg config, body publishedMediaPrepareRequest, now time.Time) (any, error) {
	token, ok := parsePublishedVideoPlaybackURL(body.PlaybackURL, body.NoteID, body.PostID, body.Target, body.Index)
	if cfg.VideoPlaybackMode != "nativeProxy" || !ok {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	if !processNativeVideoLimiter.Allow(req.UserID(), now) {
		return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "video playback request limit exceeded; try again in a minute", "VIDEO_RATE_LIMITED")
	}
	rawURL, err := s.ResolveNativeVideoSource(req.Context(), body.PostID, token, body.Target, body.Index, now)
	if err != nil {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	entry, err := s.ReadPublic(req.Context(), body.PostID)
	if err != nil || entry == nil || entry.State != "ready" || entry.Post == nil || entry.FetchedAt == nil || !entry.NextFetchAt.After(now) {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	sourceKey := publishedMediaSourceKey("video-v1", body.PostID, body.Target, strconv.Itoa(body.Index), rawURL, entry.FetchedAt.UTC().Format(time.RFC3339Nano), token)
	if media, ok := processPublishedMediaRegistry.FindBySource(sourceKey, now); ok {
		return publishedMediaPrepareResult(media), nil
	}
	waitCtx, cancelWait := context.WithTimeout(req.Context(), mediaQueueWaitBudget(mediaRequestCapacity, mediaBufferCapacity, mediaUserActiveLimit, nativeVideoMaxBytes, cfg.RequestTimeout, mediaQueueWaitTimeout))
	defer cancelWait()
	releaseUser, err := processNativeVideoRequestGate.acquire(waitCtx, req.UserID(), nativeVideoMaxBytes)
	if err != nil {
		return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "video request capacity is busy; retry shortly", "VIDEO_BUSY")
	}
	defer releaseUser()
	if waitCtx.Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(waitCtx, ctx.API(), req.UserID(), body.NoteID, body.PostID) != nil {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	currentURL, err := s.ResolveNativeVideoSource(waitCtx, body.PostID, token, body.Target, body.Index, time.Now().UTC())
	currentEntry, entryErr := s.ReadPublic(waitCtx, body.PostID)
	if err != nil || entryErr != nil || currentEntry == nil || currentEntry.FetchedAt == nil || !currentEntry.FetchedAt.Equal(*entry.FetchedAt) || currentURL != rawURL {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	fetchCtx, cancelFetch := context.WithTimeout(req.Context(), cfg.RequestTimeout)
	defer cancelFetch()
	blob, releaseVideo, err := fetchVideoWithAdmission(fetchCtx, ctx.HTTP(), rawURL, nativeVideoMaxBytes, processNativeVideoAdmission)
	if err != nil {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	defer releaseVideo()
	if req.Context().Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), body.NoteID, body.PostID) != nil {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	currentURL, err = s.ResolveNativeVideoSource(req.Context(), body.PostID, token, body.Target, body.Index, time.Now().UTC())
	currentEntry, entryErr = s.ReadPublic(req.Context(), body.PostID)
	if err != nil || entryErr != nil || currentEntry == nil || currentEntry.FetchedAt == nil || !currentEntry.FetchedAt.Equal(*entry.FetchedAt) || currentURL != rawURL {
		return nil, plugin.ErrNotFound("video unavailable")
	}
	media, err := processPublishedMediaRegistry.PublishForSource(blob.ContentType, blob.Body, sourceKey, time.Now().UTC())
	if err != nil {
		return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "public media capacity is unavailable", "PUBLIC_MEDIA_BUSY")
	}
	return publishedMediaPrepareResult(media), nil
}

func publishedMediaPrepareResult(media publishedMedia) map[string]any {
	return map[string]any{
		"url": publicMediaPath(media.Token, media.Extension), "contentType": media.ContentType,
		"expiresAt": media.ExpiresAt, "size": media.Size,
	}
}

func publicMediaPath(token, extension string) string {
	return "/api/plugin/fxtwitter-preview/public-media/" + token + "." + extension
}

func parsePublishedMediaFilename(filename string) (string, string, bool) {
	if strings.Count(filename, ".") != 1 {
		return "", "", false
	}
	parts := strings.SplitN(filename, ".", 2)
	if !validPublishedMediaToken(parts[0]) {
		return "", "", false
	}
	if _, ok := publishedMediaExtension(map[string]string{
		"jpg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp", "avif": "image/avif", "mp4": "video/mp4",
	}[parts[1]]); !ok {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func parsePublishedVideoPlaybackURL(raw, noteID, postID, target string, index int) (string, bool) {
	if raw == "" || !statusIDPattern.MatchString(postID) || noteID == "" || index < 0 || index >= maxFxVideoItems || (target != "main" && target != "quote") {
		return "", false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(values) != 2 || len(values["noteId"]) != 1 || len(values["token"]) != 1 || values.Get("noteId") != noteID {
		return "", false
	}
	token := values.Get("token")
	if !embedNoncePattern.MatchString(token) || raw != videoPlaybackPath(postID, target, index, noteID, token) {
		return "", false
	}
	return token, true
}

func validPublishedMediaToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
