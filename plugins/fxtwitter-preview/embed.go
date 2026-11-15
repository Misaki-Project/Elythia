// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"
)

var embedNoncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type embedTarget struct {
	Provider string
	PostID   string
	VideoID  string
}

func (s *store) ResolveEmbed(ctx context.Context, postID, token string, now time.Time) error {
	_, err := s.ResolveEmbedTarget(ctx, postID, token, "main", now)
	return err
}

func (s *store) ResolveEmbedTarget(ctx context.Context, postID, token, target string, now time.Time) (embedTarget, error) {
	if !statusIDPattern.MatchString(postID) || !embedNoncePattern.MatchString(token) {
		return embedTarget{}, errors.New("invalid embed capability")
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT payload FROM public_post_cache
		WHERE post_id = $1 AND state = 'ready' AND embed_token = $2
		  AND embed_expires_at > $3 AND next_fetch_at > $3
	`, postID, token, now).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return embedTarget{}, errors.New("embed capability unavailable")
	}
	if err != nil {
		return embedTarget{}, err
	}
	var post PublicPost
	if err := json.Unmarshal(payload, &post); err != nil {
		return embedTarget{}, errors.New("embed payload unavailable")
	}
	var video *PublicVideo
	targetPostID := post.ID
	switch target {
	case "main":
		video = post.Video
	case "quote":
		if post.Quote == nil {
			return embedTarget{}, errors.New("embed target unavailable")
		}
		video = post.Quote.Video
		targetPostID = post.Quote.ID
	default:
		return embedTarget{}, errors.New("invalid embed target")
	}
	if !playableVideo(video) || !statusIDPattern.MatchString(targetPostID) {
		return embedTarget{}, errors.New("embed target unavailable")
	}
	return embedTarget{Provider: video.Provider, PostID: targetPostID, VideoID: video.ProviderVideoID}, nil
}

func playableVideo(video *PublicVideo) bool {
	if video == nil {
		return false
	}
	switch video.Provider {
	case "x":
		return video.ProviderVideoID == ""
	default:
		return false
	}
}

func buildEmbedURL(postID, theme, lang, nonce string) (string, error) {
	if !statusIDPattern.MatchString(postID) || !embedNoncePattern.MatchString(nonce) {
		return "", errors.New("invalid shell identifier")
	}
	if theme != "light" && theme != "dark" {
		return "", errors.New("invalid shell theme")
	}
	if lang != "en" && lang != "ja" {
		return "", errors.New("invalid shell language")
	}
	query := url.Values{
		"id":         {postID},
		"embedId":    {nonce},
		"hideCard":   {"false"},
		"hideThread": {"false"},
		"lang":       {lang},
		"theme":      {theme},
		"dnt":        {"true"},
	}
	return "https://platform.twitter.com/embed/index.html?" + query.Encode(), nil
}
