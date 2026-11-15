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

func routes(ctx plugin.Context, r plugin.Router) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	s := newStore(ctx.Storage().DB(), cfg)
	registerPublishedMediaRoutes(ctx, r, s, cfg)

	requireUser := func(req plugin.Request) error {
		if req.UserID() == "" {
			return plugin.NewCodedStatusError(http.StatusUnauthorized, "login required", "LOGIN_REQUIRED")
		}
		return nil
	}

	r.POST("/capabilities", func(req plugin.Request) (any, error) {
		canUse := req.UserID() != "" && checkedPreviewAllowed(req)
		return map[string]any{
			"externalFetchEnabled":   cfg.ExternalFetchEnabled,
			"canUseFxTwitterPreview": canUse,
			"refetchIntervalSeconds": int(cfg.RefetchInterval.Seconds()),
			"videoPlaybackMode":      cfg.VideoPlaybackMode,
			"mediaMode":              "sameOriginProxy",
		}, nil
	})

	r.POST("/preview", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		var body struct {
			AuthToken string `json:"i,omitempty"`
			NoteID    string `json:"noteId"`
			PostID    string `json:"postId"`
		}
		if err := plugin.BindStrict(req, &body); err != nil || body.NoteID == "" || !statusIDPattern.MatchString(body.PostID) {
			return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "invalid noteId or postId", "INVALID_REQUEST")
		}
		if !cfg.ExternalFetchEnabled {
			return map[string]any{"state": "disabled"}, nil
		}
		allowed, budget, policyErr := checkedPreviewPolicy(req)
		if policyErr != nil {
			return nil, plugin.NewCodedStatusError(http.StatusInternalServerError, "preview policy resolution failed", "POLICY_UNAVAILABLE")
		}
		if !allowed {
			return nil, plugin.NewCodedStatusError(http.StatusForbidden, "Fx preview is not permitted", "PREVIEW_FORBIDDEN")
		}
		if err := verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), body.NoteID, body.PostID); err != nil {
			switch {
			case errors.Is(err, errLoginRequired):
				return nil, plugin.NewCodedStatusError(http.StatusUnauthorized, "login required", "LOGIN_REQUIRED")
			case errors.Is(err, errLinkNotInNote):
				return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "status link is not in note", "LINK_NOT_IN_NOTE")
			default:
				return nil, plugin.NewCodedStatusError(http.StatusNotFound, "note is unavailable", "NOTE_UNAVAILABLE")
			}
		}
		now := time.Now().UTC()
		entry, err := s.ReadPublic(req.Context(), body.PostID)
		if err != nil {
			return nil, err
		}
		if entry != nil && entry.State != "pending" && entry.NextFetchAt.After(now) {
			if cfg.VideoPlaybackMode == "nativeProxy" && entry.State == "ready" && entry.Post != nil && (entry.Post.HasVideo || (entry.Post.Quote != nil && entry.Post.Quote.HasVideo)) {
				needsMetadata, err := s.NeedsNativeVideoMetadata(req.Context(), body.PostID)
				if err != nil {
					return nil, err
				}
				if needsMetadata {
					upgrade, err := s.AcquireNativeMetadataRefreshWithBudget(req.Context(), body.PostID, req.UserID(), budget, now)
					if errors.Is(err, errRateLimited) {
						return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "cold fetch rate limit exceeded", "RATE_LIMITED")
					}
					if err != nil {
						return nil, err
					}
					if upgrade != nil {
						enqueueTimeout := min(cfg.RequestTimeout, 5*time.Second)
						enqueueCtx, cancelEnqueue := context.WithTimeout(context.WithoutCancel(req.Context()), enqueueTimeout)
						enqueueErr := ctx.Queue().Enqueue(enqueueCtx, "fetch-post", upgrade, plugin.WithMaxAttempts(1))
						cancelEnqueue()
						if enqueueErr != nil {
							cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(req.Context()), enqueueTimeout)
							defer cancelCleanup()
							if _, cleanupErr := s.CompleteUnavailable(cleanupCtx, *upgrade, "network", time.Now().UTC()); cleanupErr != nil {
								ctx.Logger().Warn("failed to release Fx fetch after native video metadata enqueue error", "postId", upgrade.PostID, "err", cleanupErr)
							}
							return nil, enqueueErr
						}
						return map[string]any{"state": "pending", "nextPollAt": now.Add(2 * time.Second)}, nil
					}
					entry, err = s.ReadPublic(req.Context(), body.PostID)
					if err != nil {
						return nil, err
					}
				}
			}
			if entry != nil && entry.State == "pending" {
				nextPoll := now.Add(2 * time.Second)
				entry.NextPollAt = &nextPoll
			}
			return publicEntryForMode(entry, body.NoteID, cfg.VideoPlaybackMode), nil
		}
		won, err := s.AcquireFetchWithBudget(req.Context(), body.PostID, req.UserID(), budget, now)
		if errors.Is(err, errRateLimited) {
			return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "cold fetch rate limit exceeded", "RATE_LIMITED")
		}
		if err != nil {
			return nil, err
		}
		if won == nil {
			entry, err = s.ReadPublic(req.Context(), body.PostID)
			if err != nil {
				return nil, err
			}
			if entry == nil {
				return map[string]any{"state": "pending", "nextPollAt": now.Add(2 * time.Second)}, nil
			}
			if entry.State == "pending" {
				nextPoll := now.Add(2 * time.Second)
				entry.NextPollAt = &nextPoll
			}
			return publicEntryForMode(entry, body.NoteID, cfg.VideoPlaybackMode), nil
		}
		if err := ctx.Queue().Enqueue(req.Context(), "fetch-post", won, plugin.WithMaxAttempts(1)); err != nil {
			_, _ = s.CompleteUnavailable(req.Context(), *won, "enqueue", now)
			return nil, err
		}
		return map[string]any{"state": "pending", "nextPollAt": now.Add(2 * time.Second)}, nil
	})

	r.GET("/media/:postId/:kind/:index", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !checkedPreviewAllowed(req) {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		if !cfg.ExternalFetchEnabled {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		noteID := req.Query("noteId")
		postID := req.Param("postId")
		if noteID == "" || verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), noteID, postID) != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		now := time.Now().UTC()
		if !processMediaRequestLimiter.Allow(req.UserID(), now) {
			return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "media request rate limit exceeded; try again in a minute", "MEDIA_RATE_LIMITED")
		}
		waitBudget := mediaQueueWaitBudget(mediaRequestCapacity, mediaBufferCapacity, mediaUserActiveLimit, cfg.StaticImageMaxBytes, cfg.RequestTimeout, mediaQueueWaitTimeout)
		waitCtx, cancelWait := context.WithTimeout(req.Context(), waitBudget)
		defer cancelWait()
		releaseUser, err := processMediaRequestGate.acquire(waitCtx, req.UserID(), cfg.StaticImageMaxBytes)
		if err != nil {
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "media request capacity is busy; retry shortly", "MEDIA_BUSY")
		}
		if waitCtx.Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(waitCtx, ctx.API(), req.UserID(), noteID, postID) != nil {
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		rawURL, err := s.ResolveMedia(waitCtx, postID, req.Param("kind"), req.Param("index"), time.Now().UTC())
		if err != nil || waitCtx.Err() != nil {
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		fetchCtx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()
		if req.Context().Done() == nil {
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		fetchPoster := req.Param("kind") == "poster" || req.Param("kind") == "quote-poster"
		var blob plugin.Blob
		var releaseMedia func()
		if fetchPoster {
			blob, releaseMedia, err = fetchPosterWithAdmission(fetchCtx, ctx.HTTP(), rawURL, cfg.StaticImageMaxBytes, processMediaAdmission)
		} else {
			blob, releaseMedia, err = fetchMediaWithAdmission(fetchCtx, ctx.HTTP(), rawURL, cfg.StaticImageMaxBytes, processMediaAdmission)
		}
		if err != nil {
			releaseUser()
			ctx.Logger().Warn("Fx preview media unavailable", "postId", req.Param("postId"), "kind", req.Param("kind"), "err", err)
			return nil, plugin.ErrNotFound("media unavailable")
		}
		if req.Context().Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), noteID, postID) != nil {
			releaseMedia()
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		currentURL, resolveErr := s.ResolveMedia(req.Context(), postID, req.Param("kind"), req.Param("index"), time.Now().UTC())
		if resolveErr != nil || currentURL != rawURL {
			releaseMedia()
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		releaseMediaWhenRequestEnds(req.Context(), releaseMedia, releaseUser)
		return blob, nil
	})

	r.GET("/media/video/:postId/:target/:index", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !cfg.ExternalFetchEnabled || cfg.VideoPlaybackMode != "nativeProxy" {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		if !checkedPreviewAllowed(req) {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		postID, noteID, target, token := req.Param("postId"), req.Query("noteId"), req.Param("target"), req.Query("token")
		index, parseErr := strconv.Atoi(req.Param("index"))
		if noteID == "" || parseErr != nil || index < 0 || index >= maxFxVideoItems {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		if verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), noteID, postID) != nil {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		if !processNativeVideoLimiter.Allow(req.UserID(), time.Now().UTC()) {
			return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "video playback request limit exceeded; try again in a minute", "VIDEO_RATE_LIMITED")
		}
		waitCtx, cancelWait := context.WithTimeout(req.Context(), mediaQueueWaitBudget(mediaRequestCapacity, mediaBufferCapacity, mediaUserActiveLimit, nativeVideoMaxBytes, cfg.RequestTimeout, mediaQueueWaitTimeout))
		defer cancelWait()
		releaseUser, err := processNativeVideoRequestGate.acquire(waitCtx, req.UserID(), nativeVideoMaxBytes)
		if err != nil {
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "video playback capacity is busy; retry shortly", "VIDEO_BUSY")
		}
		if waitCtx.Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(waitCtx, ctx.API(), req.UserID(), noteID, postID) != nil {
			releaseUser()
			return nil, plugin.ErrNotFound("video unavailable")
		}
		rawURL, err := s.ResolveNativeVideoSource(waitCtx, postID, token, target, index, time.Now().UTC())
		if err != nil || waitCtx.Err() != nil {
			releaseUser()
			return nil, plugin.ErrNotFound("video unavailable")
		}
		fetchCtx, cancelFetch := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancelFetch()
		blob, releaseVideo, err := fetchVideoWithAdmission(fetchCtx, ctx.HTTP(), rawURL, nativeVideoMaxBytes, processNativeVideoAdmission)
		if err != nil {
			releaseUser()
			if errors.Is(err, errNativeVideoTooLarge) {
				return nil, plugin.NewCodedStatusError(http.StatusRequestEntityTooLarge, "video exceeds the in-app playback limit", "VIDEO_TOO_LARGE")
			}
			return nil, plugin.ErrNotFound("video unavailable")
		}
		if req.Context().Err() != nil || !checkedPreviewAllowed(req) || verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), noteID, postID) != nil {
			releaseVideo()
			releaseUser()
			return nil, plugin.ErrNotFound("video unavailable")
		}
		currentURL, resolveErr := s.ResolveNativeVideoSource(req.Context(), postID, token, target, index, time.Now().UTC())
		if resolveErr != nil || currentURL != rawURL {
			releaseVideo()
			releaseUser()
			return nil, plugin.ErrNotFound("video unavailable")
		}
		releaseMediaWhenRequestEnds(req.Context(), releaseVideo, releaseUser)
		return blob, nil
	})

	r.POST("/embed/shell", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !cfg.ExternalFetchEnabled || cfg.VideoPlaybackMode != "officialEmbed" {
			return nil, plugin.ErrNotFound("embed unavailable")
		}
		if !checkedPreviewAllowed(req) {
			return nil, plugin.ErrNotFound("embed unavailable")
		}
		var body struct {
			AuthToken string `json:"i,omitempty"`
			NoteID    string `json:"noteId"`
			PostID    string `json:"postId"`
			Target    string `json:"target"`
			Token     string `json:"token"`
			Theme     string `json:"theme"`
			Lang      string `json:"lang"`
			Nonce     string `json:"nonce"`
		}
		if err := plugin.BindStrict(req, &body); err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid embed request")
		}
		if body.Target == "" {
			body.Target = "main"
		}
		if err := verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), body.NoteID, body.PostID); err != nil {
			return nil, plugin.ErrNotFound("embed unavailable")
		}
		target, err := s.ResolveEmbedTarget(req.Context(), body.PostID, body.Token, body.Target, time.Now().UTC())
		if err != nil {
			return nil, plugin.ErrNotFound("embed unavailable")
		}
		if !embedNoncePattern.MatchString(body.Nonce) || (body.Theme != "light" && body.Theme != "dark") || (body.Lang != "en" && body.Lang != "ja") {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid embed options")
		}
		var embedURL string
		switch target.Provider {
		case "x":
			embedURL, err = buildEmbedURL(target.PostID, body.Theme, body.Lang, body.Nonce)
		default:
			err = errors.New("unsupported embed provider")
		}
		if err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid embed options")
		}
		return map[string]any{"url": embedURL}, nil
	})

	r.POST("/admin/audit/list", func(req plugin.Request) (any, error) {
		if !req.IsModerator() {
			return nil, plugin.Errorf(http.StatusForbidden, "moderator permission required")
		}
		var body struct {
			AuthToken string `json:"i,omitempty"`
			PostID    string `json:"postId"`
		}
		if err := plugin.BindStrict(req, &body); err != nil || !statusIDPattern.MatchString(body.PostID) {
			return nil, plugin.Errorf(http.StatusBadRequest, "valid postId required")
		}
		items, err := s.ListAudit(req.Context(), body.PostID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	})

	r.POST("/admin/audit/delete", func(req plugin.Request) (any, error) {
		if !req.IsModerator() {
			return nil, plugin.Errorf(http.StatusForbidden, "moderator permission required")
		}
		var body struct {
			AuthToken  string `json:"i,omitempty"`
			Target     string `json:"target"`
			PostID     string `json:"postId"`
			SnapshotID *int64 `json:"snapshotId"`
			Reason     string `json:"reason"`
			Confirm    bool   `json:"confirm"`
		}
		if err := plugin.BindStrict(req, &body); err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "target, selection, reason, and confirmation are required")
		}
		validSelection := statusIDPattern.MatchString(body.PostID) || (body.Target == "audit" && body.PostID == "" && body.SnapshotID != nil)
		if !validSelection ||
			(body.Target != "public" && body.Target != "audit" && body.Target != "both") ||
			strings.TrimSpace(body.Reason) == "" || !body.Confirm {
			return nil, plugin.Errorf(http.StatusBadRequest, "target, selection, reason, and confirmation are required")
		}
		count, err := s.Delete(req.Context(), req.UserID(), body.Target, body.PostID, body.SnapshotID, strings.TrimSpace(body.Reason))
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": count}, nil
	})

	r.POST("/admin/status", func(req plugin.Request) (any, error) {
		if !req.IsModerator() {
			return nil, plugin.Errorf(http.StatusForbidden, "moderator permission required")
		}
		return map[string]any{
			"externalFetchEnabled":    cfg.ExternalFetchEnabled,
			"refetchIntervalSeconds":  int(cfg.RefetchInterval.Seconds()),
			"auditRetentionDays":      int(cfg.AuditRetention.Hours() / 24),
			"auditRetentionUnlimited": cfg.AuditRetention == 0,
			"canManageRateLimits":     req.IsAdministrator(),
		}, nil
	})

	r.POST("/admin/rate-limit-settings", func(req plugin.Request) (any, error) {
		if !req.IsAdministrator() {
			return nil, plugin.Errorf(http.StatusForbidden, "administrator permission required")
		}
		var body struct {
			AuthToken string `json:"i,omitempty"`
		}
		if err := plugin.BindStrict(req, &body); err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid request")
		}
		return s.ReadRateLimitSettingsResponse(req.Context())
	})

	r.POST("/admin/rate-limit-settings/update", func(req plugin.Request) (any, error) {
		if !req.IsAdministrator() {
			return nil, plugin.Errorf(http.StatusForbidden, "administrator permission required")
		}
		var body struct {
			AuthToken     string `json:"i,omitempty"`
			Revision      int64  `json:"revision"`
			GlobalPerHour int    `json:"globalPerHour"`
		}
		if err := plugin.BindStrict(req, &body); err != nil || body.Revision < 1 {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid rate limit settings")
		}
		settings := rateLimitSettings{
			Revision: body.Revision, GlobalPerHour: body.GlobalPerHour,
		}
		result, err := s.UpdateRateLimitSettings(req.Context(), req.UserID(), settings)
		switch {
		case errors.Is(err, errSettingsConflict):
			return nil, plugin.Errorf(http.StatusConflict, "settings changed; reload and try again")
		case err != nil:
			return nil, plugin.Errorf(http.StatusBadRequest, "%s", err.Error())
		default:
			return result, nil
		}
	})
	return nil
}

func publicEntry(entry *CacheEntry, noteID string) *CacheEntry {
	return publicEntryForMode(entry, noteID, "officialEmbed")
}

func publicEntryForMode(entry *CacheEntry, noteID, videoPlaybackMode string) *CacheEntry {
	if entry == nil || entry.Post == nil {
		return entry
	}
	copyEntry := *entry
	post := *entry.Post
	post.Author.AvatarURL = mediaPath(post.ID, "avatar", 0, noteID)
	post.Images = append([]PublicImage{}, post.Images...)
	for i := range post.Images {
		post.Images[i].URL = mediaPath(post.ID, "image", i, noteID)
	}
	playbackToken := ""
	if videoPlaybackMode == "nativeProxy" {
		playbackToken = entry.PlaybackToken
	}
	post.Videos = rewriteVideoMetadata(post.Videos, post.ID, "main", noteID, playbackToken)
	if post.Video != nil {
		video := *post.Video
		video.PosterURL = mediaPath(post.ID, "poster", 0, noteID)
		if videoPlaybackMode == "nativeProxy" {
			video.EmbedToken = ""
			video.EmbedExpiresAt = nil
		} else if videoPlaybackMode == "linkOnly" {
			video.EmbedToken = ""
			video.EmbedExpiresAt = nil
		}
		post.Video = &video
	}
	if post.Quote != nil {
		quote := *post.Quote
		quote.Author.AvatarURL = mediaPath(post.ID, "quote-avatar", 0, noteID)
		quote.Images = append([]PublicImage{}, post.Quote.Images...)
		for i := range quote.Images {
			quote.Images[i].URL = mediaPath(post.ID, "quote-image", i, noteID)
		}
		quote.Videos = rewriteVideoMetadata(quote.Videos, post.ID, "quote", noteID, playbackToken)
		if post.Quote.Video != nil {
			video := *post.Quote.Video
			video.PosterURL = mediaPath(post.ID, "quote-poster", 0, noteID)
			if videoPlaybackMode == "nativeProxy" {
				video.EmbedToken = ""
				video.EmbedExpiresAt = nil
			} else if videoPlaybackMode == "linkOnly" {
				video.EmbedToken = ""
				video.EmbedExpiresAt = nil
			}
			quote.Video = &video
		}
		post.Quote = &quote
	}
	copyEntry.Post = &post
	return &copyEntry
}

func rewriteVideoMetadata(videos []PublicVideoMetadata, postID, target, noteID, token string) []PublicVideoMetadata {
	result := append([]PublicVideoMetadata(nil), videos...)
	for i := range result {
		result[i].PosterURL = mediaPath(postID, videoPosterKind(target), i, noteID)
		result[i].PlaybackURL = ""
		if result[i].Provider == "x" && result[i].PlaybackAvailable && token != "" {
			result[i].PlaybackURL = videoPlaybackPath(postID, target, i, noteID, token)
		}
	}
	return result
}

func videoPosterKind(target string) string {
	if target == "quote" {
		return "quote-poster"
	}
	return "poster"
}

func videoPlaybackPath(postID, target string, index int, noteID, token string) string {
	return "/api/plugin/fxtwitter-preview/media/video/" + url.PathEscape(postID) + "/" + target + "/" + strconv.Itoa(index) + "?noteId=" + url.QueryEscape(noteID) + "&token=" + url.QueryEscape(token)
}

func mediaPath(postID, kind string, index int, noteID string) string {
	return "/api/plugin/fxtwitter-preview/media/" + postID + "/" + kind + "/" + strconv.Itoa(index) + "?noteId=" + url.QueryEscape(noteID)
}

func checkedPreviewAllowed(req plugin.Request) bool {
	allowed, _, err := checkedPreviewPolicy(req)
	return err == nil && allowed
}

func checkedPreviewPolicy(req plugin.Request) (bool, coldFetchBudget, error) {
	policyReq, ok := req.(plugin.CheckedRolePolicyRequest)
	if !ok || req.UserID() == "" {
		return false, coldFetchBudget{}, errors.New("checked role policy lookup is unavailable")
	}
	policies, err := policyReq.CheckedRolePolicies("canUseFxTwitterPreview", "fxTwitterColdFetchesPerHour", "fxTwitterColdFetchesPerDay")
	if err != nil {
		return false, coldFetchBudget{}, err
	}
	allowed, ok := policies["canUseFxTwitterPreview"].(bool)
	hour, hourOK := policies["fxTwitterColdFetchesPerHour"].(int)
	day, dayOK := policies["fxTwitterColdFetchesPerDay"].(int)
	if !ok || !hourOK || !dayOK || hour < 1 || hour > 10000 || day < 1 || day > 100000 {
		return false, coldFetchBudget{}, errors.New("checked FxTwitter role policies are invalid")
	}
	return allowed, coldFetchBudget{PerHour: hour, PerDay: day}, nil
}
