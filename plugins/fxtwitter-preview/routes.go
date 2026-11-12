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

	requireUser := func(req plugin.Request) error {
		if req.UserID() == "" {
			return plugin.NewCodedStatusError(http.StatusUnauthorized, "login required", "LOGIN_REQUIRED")
		}
		return nil
	}

	requirePreviewPolicy := func(req plugin.Request) (coldFetchBudget, error) {
		allowed, budget, err := checkedPreviewPolicy(req)
		if err != nil {
			return coldFetchBudget{}, plugin.NewCodedStatusError(http.StatusInternalServerError, "preview policy resolution failed", "POLICY_UNAVAILABLE")
		}
		if !allowed {
			return coldFetchBudget{}, plugin.NewCodedStatusError(http.StatusForbidden, "Fx preview is not permitted", "PREVIEW_FORBIDDEN")
		}
		return budget, nil
	}

	r.POST("/capabilities", func(req plugin.Request) (any, error) {
		allowed := false
		if req.UserID() != "" {
			allowed, _, _ = checkedPreviewPolicy(req)
		}
		return map[string]any{
			"externalFetchEnabled":   cfg.ExternalFetchEnabled,
			"canUseFxTwitterPreview": allowed,
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
		if err := req.BindStrict(&body); err != nil || body.NoteID == "" || !statusIDPattern.MatchString(body.PostID) {
			return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "invalid noteId or postId", "INVALID_REQUEST")
		}
		if !cfg.ExternalFetchEnabled {
			return map[string]any{"state": "disabled"}, nil
		}
		budget, err := requirePreviewPolicy(req)
		if err != nil {
			return nil, err
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
						enqueueCtx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), min(cfg.RequestTimeout, 5*time.Second))
						enqueueErr := ctx.Queue().Enqueue(enqueueCtx, "fetch-post", upgrade, plugin.WithMaxAttempts(1))
						cancel()
						if enqueueErr != nil {
							cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(req.Context()), min(cfg.RequestTimeout, 5*time.Second))
							defer cancelCleanup()
							if cleanupErr := s.CancelFetch(cleanupCtx, *upgrade, "enqueue", time.Now().UTC()); cleanupErr != nil {
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
					if entry != nil && entry.State == "pending" {
						nextPoll := now.Add(2 * time.Second)
						entry.NextPollAt = &nextPoll
					}
				}
			}
			return publicEntry(entry, body.NoteID), nil
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
			return publicEntry(entry, body.NoteID), nil
		}
		enqueueTimeout := cfg.RequestTimeout
		if enqueueTimeout > 5*time.Second {
			enqueueTimeout = 5 * time.Second
		}
		enqueueCtx, cancelEnqueue := context.WithTimeout(context.WithoutCancel(req.Context()), enqueueTimeout)
		enqueueErr := ctx.Queue().Enqueue(enqueueCtx, "fetch-post", won, plugin.WithMaxAttempts(1))
		cancelEnqueue()
		if enqueueErr != nil {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(req.Context()), enqueueTimeout)
			defer cancelCleanup()
			if cleanupErr := s.CancelFetch(cleanupCtx, *won, "enqueue", time.Now().UTC()); cleanupErr != nil {
				ctx.Logger().Warn("failed to release Fx fetch after queue error", "postId", won.PostID, "err", cleanupErr)
			}
			return nil, enqueueErr
		}
		return map[string]any{"state": "pending", "nextPollAt": now.Add(2 * time.Second)}, nil
	})

	r.GET("/media/:postId/:kind/:index", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !cfg.ExternalFetchEnabled {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		if _, err := requirePreviewPolicy(req); err != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		noteID := req.Query("noteId")
		postID := req.Param("postId")
		if noteID == "" || !statusIDPattern.MatchString(postID) {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		now := time.Now().UTC()
		if !processMediaRequestLimiter.Allow(req.UserID(), now) {
			return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "media request rate limit exceeded; try again in a minute", "MEDIA_RATE_LIMITED")
		}
		if verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), noteID, postID) != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		rawURL, fetchedAt, err := s.ResolveMediaDescriptor(req.Context(), postID, req.Param("kind"), req.Param("index"), now)
		if err != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		waitBudget := mediaQueueWaitBudget(mediaRequestCapacity, mediaBufferCapacity, mediaUserActiveLimit, cfg.StaticImageMaxBytes, cfg.RequestTimeout, mediaQueueWaitTimeout)
		waitCtx, cancelWait := context.WithTimeout(req.Context(), waitBudget)
		defer cancelWait()
		releaseUser, err := processMediaRequestGate.acquire(waitCtx, req.UserID(), cfg.StaticImageMaxBytes)
		if err != nil {
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "media request capacity is busy; retry shortly", "MEDIA_BUSY")
		}
		releaseAdmission, err := processMediaAdmission.admitWithQueue(waitCtx, cfg.StaticImageMaxBytes, true)
		if err != nil {
			releaseUser()
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "media request capacity is busy; retry shortly", "MEDIA_BUSY")
		}
		// Admission may wait behind other plugin media. Recheck policy, note ACL,
		// and the current descriptor before using even a warm cache entry.
		if waitCtx.Err() != nil {
			releaseAdmission()
			releaseUser()
			return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "media request capacity is busy; retry shortly", "MEDIA_BUSY")
		}
		if _, err := requirePreviewPolicy(req); err != nil || waitCtx.Err() != nil ||
			verifyVisibleNote(waitCtx, ctx.API(), req.UserID(), noteID, postID) != nil {
			releaseAdmission()
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		rawURL, fetchedAt, err = s.ResolveMediaDescriptor(waitCtx, postID, req.Param("kind"), req.Param("index"), time.Now().UTC())
		if err != nil {
			releaseAdmission()
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme != "https" || !allowedMediaHost(u.Host) || u.User != nil {
			releaseAdmission()
			releaseUser()
			return nil, plugin.ErrNotFound("media unavailable")
		}
		key := mediaCacheKey{PostID: postID, SourceURL: u.String(), MaxBytes: cfg.StaticImageMaxBytes}
		cacheable := fetchedAt != nil
		if fetchedAt != nil {
			key.FetchedAt = *fetchedAt
		}
		if cacheable {
			if blob, ok := processMediaResponseCache.Get(key, time.Now().UTC()); ok {
				blob.CacheControl = "private, no-store"
				blob.WriteTimeout = mediaResponseWriteTimeout
				releaseMediaWhenRequestEnds(req.Context(), releaseAdmission, releaseUser)
				return blob, nil
			}
		}
		fetchCtx, cancelFetch := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancelFetch()
		blob, releaseMedia, err := fetchMediaWithReservation(fetchCtx, ctx.HTTP(), u, cfg.StaticImageMaxBytes, processMediaAdmission, releaseAdmission)
		if err != nil {
			releaseUser()
			ctx.Logger().Warn("Fx preview media unavailable", "postId", postID, "kind", req.Param("kind"), "err", err)
			return nil, plugin.ErrNotFound("media unavailable")
		}
		if cacheable {
			processMediaResponseCache.Set(key, blob, time.Now().UTC())
		}
		releaseMediaWhenRequestEnds(req.Context(), releaseMedia, releaseUser)
		return blob, nil
	})

	r.POST("/media/video", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !cfg.ExternalFetchEnabled || cfg.VideoPlaybackMode != "nativeProxy" {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		if _, err := requirePreviewPolicy(req); err != nil {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		var body struct {
			AuthToken string `json:"i,omitempty"`
			NoteID    string `json:"noteId"`
			PostID    string `json:"postId"`
			Target    string `json:"target"`
			Index     int    `json:"index"`
			Token     string `json:"token"`
		}
		if err := req.BindStrict(&body); err != nil || body.NoteID == "" || !statusIDPattern.MatchString(body.PostID) || (body.Target != "main" && body.Target != "quote") || body.Index < 0 || body.Index >= maxFxMediaItems || !embedNoncePattern.MatchString(body.Token) {
			return nil, plugin.NewCodedStatusError(http.StatusBadRequest, "invalid video request", "INVALID_REQUEST")
		}
		if err := verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), body.NoteID, body.PostID); err != nil {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		rawURL, err := s.ResolveNativeVideoSource(req.Context(), body.PostID, body.Token, body.Target, body.Index, time.Now().UTC())
		if err != nil {
			return nil, plugin.ErrNotFound("video unavailable")
		}
		if !processNativeVideoLimiter.Allow(req.UserID(), time.Now().UTC()) {
			return nil, plugin.NewCodedStatusError(http.StatusTooManyRequests, "video playback request limit exceeded; try again in a minute", "VIDEO_RATE_LIMITED")
		}
		fetchCtx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()
		blob, releaseVideo, err := fetchVideoWithAdmission(fetchCtx, ctx.HTTP(), rawURL, nativeVideoMaxBytes, processNativeVideoAdmission)
		if err != nil {
			ctx.Logger().Warn("Fx native video unavailable", "postId", body.PostID, "target", body.Target, "index", body.Index, "err", err)
			if errors.Is(err, errNativeVideoTooLarge) {
				return nil, plugin.NewCodedStatusError(http.StatusRequestEntityTooLarge, "video exceeds the 32 MiB in-app playback limit; open the post on X", "VIDEO_TOO_LARGE")
			}
			if errors.Is(err, errMediaBusy) {
				return nil, plugin.NewCodedStatusError(http.StatusServiceUnavailable, "video playback capacity is busy; try again later", "VIDEO_BUSY")
			}
			return nil, plugin.NewCodedStatusError(http.StatusBadGateway, "video is unavailable for in-app playback; open the post on X", "VIDEO_UNAVAILABLE")
		}
		if req.Context().Done() == nil {
			releaseVideo()
		} else {
			context.AfterFunc(req.Context(), releaseVideo)
		}
		return blob, nil
	})

	r.POST("/embed/shell", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !cfg.ExternalFetchEnabled || (cfg.VideoPlaybackMode != "officialEmbed" && cfg.VideoPlaybackMode != "nativeProxy") {
			return nil, plugin.ErrNotFound("embed unavailable")
		}
		if _, err := requirePreviewPolicy(req); err != nil {
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
		if err := req.BindStrict(&body); err != nil {
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
		if cfg.VideoPlaybackMode == "nativeProxy" && target.Provider != "youtube" {
			return nil, plugin.ErrNotFound("embed unavailable")
		}
		if !embedNoncePattern.MatchString(body.Nonce) || (body.Theme != "light" && body.Theme != "dark") || (body.Lang != "en" && body.Lang != "ja") {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid embed options")
		}
		var embedURL string
		switch target.Provider {
		case "x":
			embedURL, err = buildEmbedURL(target.PostID, body.Theme, body.Lang, body.Nonce)
		case "youtube":
			embedURL, err = buildYouTubeEmbedURL(target.VideoID)
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
		if err := req.BindStrict(&body); err != nil || !statusIDPattern.MatchString(body.PostID) {
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
		if err := req.BindStrict(&body); err != nil {
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
		if err := req.BindStrict(&body); err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid request")
		}
		return s.ReadRateLimitSettingsResponse(req.Context())
	})

	r.POST("/admin/global-rate-limit", func(req plugin.Request) (any, error) {
		if !req.IsAdministrator() {
			return nil, plugin.Errorf(http.StatusForbidden, "administrator permission required")
		}
		var body struct {
			AuthToken string `json:"i,omitempty"`
		}
		if err := req.BindStrict(&body); err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid request")
		}
		return s.ReadGlobalRateLimit(req.Context())
	})

	r.POST("/admin/global-rate-limit/update", func(req plugin.Request) (any, error) {
		if !req.IsAdministrator() {
			return nil, plugin.Errorf(http.StatusForbidden, "administrator permission required")
		}
		var body struct {
			AuthToken     string `json:"i,omitempty"`
			Revision      int64  `json:"revision"`
			GlobalPerHour int    `json:"globalPerHour"`
		}
		if err := req.BindStrict(&body); err != nil || body.Revision < 1 {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid global rate limit settings")
		}
		result, err := s.UpdateGlobalRateLimit(req.Context(), req.UserID(), body.Revision, body.GlobalPerHour)
		if errors.Is(err, errSettingsConflict) {
			return nil, plugin.Errorf(http.StatusConflict, "settings changed; reload and try again")
		}
		if err != nil {
			return nil, plugin.Errorf(http.StatusBadRequest, "%s", err.Error())
		}
		return result, nil
	})

	return nil
}

func checkedPreviewPolicy(req plugin.Request) (bool, coldFetchBudget, error) {
	if req.UserID() == "" {
		return false, coldFetchBudget{}, nil
	}
	policyReq, ok := req.(plugin.CheckedRolePolicyRequest)
	if !ok {
		return false, coldFetchBudget{}, errors.New("checked role policy lookup is unavailable")
	}
	policies, err := policyReq.CheckedRolePolicies("canUseFxTwitterPreview", "fxTwitterColdFetchesPerHour", "fxTwitterColdFetchesPerDay")
	if err != nil {
		return false, coldFetchBudget{}, err
	}
	allowed, allowOK := policies["canUseFxTwitterPreview"].(bool)
	hour, hourOK := policies["fxTwitterColdFetchesPerHour"].(int)
	day, dayOK := policies["fxTwitterColdFetchesPerDay"].(int)
	if !allowOK || !hourOK || !dayOK || hour < 1 || hour > 10000 || day < 1 || day > 100000 {
		return false, coldFetchBudget{}, errors.New("checked FxTwitter role policies are invalid")
	}
	return allowed, coldFetchBudget{PerHour: hour, PerDay: day}, nil
}

func publicEntry(entry *CacheEntry, noteID string) *CacheEntry {
	if entry == nil {
		return entry
	}
	copyEntry := *entry
	copyEntry.Error = ""
	copyEntry.RetryAt = nil
	if entry.State == "unavailable" || entry.State == "stale" {
		copyEntry.Error = publicFetchError(entry.ErrorClass)
		if !entry.NextFetchAt.IsZero() {
			retryAt := entry.NextFetchAt.UTC()
			copyEntry.RetryAt = &retryAt
		}
	}
	if entry.Post == nil {
		return &copyEntry
	}
	post := *entry.Post
	post.Author.AvatarURL = mediaPath(post.ID, "avatar", 0, noteID)
	post.Images = append([]PublicImage{}, post.Images...)
	for i := range post.Images {
		post.Images[i].URL = mediaPath(post.ID, "image", i, noteID)
	}
	if post.Video != nil && post.Video.PosterURL != "" {
		video := *post.Video
		video.PosterURL = mediaPath(post.ID, "poster", 0, noteID)
		post.Video = &video
	}
	post.Videos = append([]PublicVideoMetadata{}, post.Videos...)
	for i := range post.Videos {
		if post.Videos[i].PosterURL != "" {
			post.Videos[i].PosterURL = mediaPath(post.ID, "poster", i, noteID)
		}
	}
	if post.Quote != nil {
		quote := *post.Quote
		quote.Author.AvatarURL = mediaPath(post.ID, "quote-avatar", 0, noteID)
		quote.Images = append([]PublicImage{}, post.Quote.Images...)
		for i := range quote.Images {
			quote.Images[i].URL = mediaPath(post.ID, "quote-image", i, noteID)
		}
		if post.Quote.Video != nil && post.Quote.Video.PosterURL != "" {
			video := *post.Quote.Video
			video.PosterURL = mediaPath(post.ID, "quote-poster", 0, noteID)
			quote.Video = &video
		}
		quote.Videos = append([]PublicVideoMetadata{}, quote.Videos...)
		for i := range quote.Videos {
			if quote.Videos[i].PosterURL != "" {
				quote.Videos[i].PosterURL = mediaPath(post.ID, "quote-poster", i, noteID)
			}
		}
		post.Quote = &quote
	}
	copyEntry.Post = &post
	return &copyEntry
}

func publicFetchError(class string) string {
	switch class {
	case "network", "read", "timeout", "upstream_429", "upstream_5xx":
		return "temporary"
	case "tombstone_private":
		return "private"
	case "fx_private":
		return "private"
	case "tombstone_deleted":
		return "not-found"
	case "fx_not_found":
		return "not-found"
	default:
		// Status-only 401/403/404 responses, legacy class names, and unverified
		// tombstones do not establish whether a post is private or deleted.
		return "unavailable"
	}
}

func mediaPath(postID, kind string, index int, noteID string) string {
	return "/api/plugin/fxtwitter-preview/media/" + postID + "/" + kind + "/" + strconv.Itoa(index) + "?noteId=" + url.QueryEscape(noteID)
}
