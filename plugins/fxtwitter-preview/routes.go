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

	r.POST("/capabilities", func(req plugin.Request) (any, error) {
		return map[string]any{
			"externalFetchEnabled":   cfg.ExternalFetchEnabled,
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
		if entry != nil && entry.NextFetchAt.After(now) {
			if entry.State == "pending" {
				nextPoll := now.Add(2 * time.Second)
				entry.NextPollAt = &nextPoll
			}
			return publicEntry(entry, body.NoteID), nil
		}
		roleReq, ok := req.(plugin.ActiveRoleRequest)
		if !ok {
			return nil, errors.New("active role lookup is unavailable")
		}
		roleIDs, roleErr := roleReq.ActiveRoleIDs()
		if roleErr != nil {
			return nil, roleErr
		}
		won, err := s.AcquireFetchForRoles(req.Context(), body.PostID, req.UserID(), roleIDs, now)
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
		if !cfg.ExternalFetchEnabled {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		noteID := req.Query("noteId")
		postID := req.Param("postId")
		if noteID == "" || verifyVisibleNote(req.Context(), ctx.API(), req.UserID(), noteID, postID) != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		rawURL, err := s.ResolveMedia(req.Context(), postID, req.Param("kind"), req.Param("index"), time.Now().UTC())
		if err != nil {
			return nil, plugin.ErrNotFound("media unavailable")
		}
		fetchCtx, cancel := context.WithTimeout(req.Context(), cfg.RequestTimeout)
		defer cancel()
		blob, err := fetchMedia(fetchCtx, ctx.HTTP(), rawURL, cfg.StaticImageMaxBytes)
		if err != nil {
			ctx.Logger().Warn("Fx preview media unavailable", "postId", req.Param("postId"), "kind", req.Param("kind"), "err", err)
			return nil, plugin.ErrNotFound("media unavailable")
		}
		return blob, nil
	})

	r.POST("/embed/shell", func(req plugin.Request) (any, error) {
		if err := requireUser(req); err != nil {
			return nil, err
		}
		if !cfg.ExternalFetchEnabled || cfg.VideoPlaybackMode != "officialEmbed" {
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

	r.POST("/admin/rate-limit-settings/update", func(req plugin.Request) (any, error) {
		if !req.IsAdministrator() {
			return nil, plugin.Errorf(http.StatusForbidden, "administrator permission required")
		}
		var body struct {
			AuthToken            string                  `json:"i,omitempty"`
			Revision             int64                   `json:"revision"`
			AuthenticatedPerHour int                     `json:"authenticatedPerHour"`
			AuthenticatedPerDay  int                     `json:"authenticatedPerDay"`
			GlobalPerHour        int                     `json:"globalPerHour"`
			RoleLimits           []roleRateLimitResponse `json:"roleLimits"`
		}
		if err := req.BindStrict(&body); err != nil || body.Revision < 1 {
			return nil, plugin.Errorf(http.StatusBadRequest, "invalid rate limit settings")
		}
		settings := rateLimitSettings{
			Revision: body.Revision, AuthenticatedPerHour: body.AuthenticatedPerHour,
			AuthenticatedPerDay: body.AuthenticatedPerDay, GlobalPerHour: body.GlobalPerHour,
			RoleLimits: make(map[string]coldFetchBudget, len(body.RoleLimits)),
		}
		for _, role := range body.RoleLimits {
			if _, duplicate := settings.RoleLimits[role.RoleID]; duplicate {
				return nil, plugin.Errorf(http.StatusBadRequest, "duplicate role rate limit")
			}
			settings.RoleLimits[role.RoleID] = coldFetchBudget{PerHour: role.PerHour, PerDay: role.PerDay}
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
	if post.Video != nil {
		video := *post.Video
		video.PosterURL = mediaPath(post.ID, "poster", 0, noteID)
		post.Video = &video
	}
	if post.Quote != nil {
		quote := *post.Quote
		quote.Author.AvatarURL = mediaPath(post.ID, "quote-avatar", 0, noteID)
		quote.Images = append([]PublicImage{}, post.Quote.Images...)
		for i := range quote.Images {
			quote.Images[i].URL = mediaPath(post.ID, "quote-image", i, noteID)
		}
		if post.Quote.Video != nil {
			video := *post.Quote.Video
			video.PosterURL = mediaPath(post.ID, "quote-poster", 0, noteID)
			quote.Video = &video
		}
		post.Quote = &quote
	}
	copyEntry.Post = &post
	return &copyEntry
}

func mediaPath(postID, kind string, index int, noteID string) string {
	return "/api/plugin/fxtwitter-preview/media/" + postID + "/" + kind + "/" + strconv.Itoa(index) + "?noteId=" + url.QueryEscape(noteID)
}
