// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

var statusIDPattern = regexp.MustCompile(`^[0-9]{2,20}$`)
var xUserIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)
var statusUserPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)
var youtubeVideoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
var candidateURLPattern = regexp.MustCompile(`https://[^\s<>"']+`)

var statusHosts = map[string]bool{
	"x.com": true, "www.x.com": true, "mobile.x.com": true,
	"twitter.com": true, "www.twitter.com": true, "mobile.twitter.com": true,
}

type PublicPost struct {
	ID                  string                `json:"id"`
	URL                 string                `json:"url"`
	Text                string                `json:"text"`
	TextEntities        []PublicTextEntity    `json:"textEntities,omitempty"`
	CreatedAt           string                `json:"createdAt,omitempty"`
	Author              PublicAuthor          `json:"author"`
	Likes               int64                 `json:"likes"`
	Reposts             int64                 `json:"reposts"`
	Quotes              int64                 `json:"quotes"`
	Replies             int64                 `json:"replies"`
	IsSensitive         *bool                 `json:"isSensitive,omitempty"`
	Images              []PublicImage         `json:"images"`
	HasVideo            bool                  `json:"hasVideo"`
	Video               *PublicVideo          `json:"video,omitempty"`
	Videos              []PublicVideoMetadata `json:"videos,omitempty"`
	Quote               *PublicQuotedPost     `json:"quote,omitempty"`
	privateVideoSources privateVideoSources   `json:"-"`
}

type PublicQuotedPost struct {
	ID           string                `json:"id"`
	URL          string                `json:"url"`
	Text         string                `json:"text"`
	TextEntities []PublicTextEntity    `json:"textEntities,omitempty"`
	CreatedAt    string                `json:"createdAt,omitempty"`
	Author       PublicAuthor          `json:"author"`
	IsSensitive  *bool                 `json:"isSensitive,omitempty"`
	Images       []PublicImage         `json:"images"`
	HasVideo     bool                  `json:"hasVideo"`
	Video        *PublicVideo          `json:"video,omitempty"`
	Videos       []PublicVideoMetadata `json:"videos,omitempty"`
}

type PublicAuthor struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	ScreenName string `json:"screenName"`
	AvatarURL  string `json:"avatarUrl,omitempty"`
	ProfileURL string `json:"profileUrl,omitempty"`
}

type PublicTextEntity struct {
	Kind    string `json:"kind"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Href    string `json:"href,omitempty"`
	Display string `json:"display"`
	Tag     string `json:"tag,omitempty"`
}

type PublicImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Alt    string `json:"alt,omitempty"`
}

type PublicVideo struct {
	Provider          string     `json:"provider,omitempty"`
	ProviderVideoID   string     `json:"providerVideoId,omitempty"`
	PosterURL         string     `json:"posterUrl,omitempty"`
	PosterExpiresAt   *time.Time `json:"posterExpiresAt,omitempty"`
	DurationMS        int64      `json:"durationMs,omitempty"`
	PlaybackAvailable bool       `json:"playbackAvailable,omitempty"`
	EmbedToken        string     `json:"embedToken,omitempty"`
	EmbedExpiresAt    *time.Time `json:"embedExpiresAt,omitempty"`
}

// PublicVideoMetadata describes one poster. Embed capabilities remain on the
// legacy single Video field and are never copied into this collection.
type PublicVideoMetadata struct {
	Provider          string     `json:"provider,omitempty"`
	ProviderVideoID   string     `json:"providerVideoId,omitempty"`
	PosterURL         string     `json:"posterUrl,omitempty"`
	PosterExpiresAt   *time.Time `json:"posterExpiresAt,omitempty"`
	DurationMS        int64      `json:"durationMs,omitempty"`
	PlaybackAvailable bool       `json:"playbackAvailable,omitempty"`
}

func youtubeVideoIDFromURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	var id string
	switch host {
	case "youtu.be":
		if !strings.HasPrefix(u.EscapedPath(), "/") {
			return "", false
		}
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
		if len(parts) != 1 || parts[0] == "" {
			return "", false
		}
		id, err = url.PathUnescape(parts[0])
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		switch {
		case u.EscapedPath() == "/watch":
			values, parseErr := url.ParseQuery(u.RawQuery)
			if parseErr != nil || len(values["v"]) != 1 {
				return "", false
			}
			id = values["v"][0]
		case strings.HasPrefix(u.EscapedPath(), "/shorts/"):
			parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
			if len(parts) != 2 || parts[0] != "shorts" {
				return "", false
			}
			id, err = url.PathUnescape(parts[1])
		case strings.HasPrefix(u.EscapedPath(), "/embed/"):
			parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
			if len(parts) != 2 || parts[0] != "embed" {
				return "", false
			}
			id, err = url.PathUnescape(parts[1])
		default:
			return "", false
		}
	default:
		return "", false
	}
	if err != nil || !youtubeVideoIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}

type CacheEntry struct {
	PostID      string      `json:"postId"`
	State       string      `json:"state"`
	Post        *PublicPost `json:"post,omitempty"`
	FetchedAt   *time.Time  `json:"fetchedAt,omitempty"`
	NextFetchAt time.Time   `json:"-"`
	NextPollAt  *time.Time  `json:"nextPollAt,omitempty"`
	ErrorClass  string      `json:"-"`
	Error       string      `json:"error,omitempty"`
	RetryAt     *time.Time  `json:"retryAt,omitempty"`
	Stale       bool        `json:"stale,omitempty"`
}

func statusIDFromURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !statusHosts[strings.ToLower(u.Hostname())] {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 3 || parts[1] != "status" {
		return "", false
	}
	username, err := url.PathUnescape(parts[0])
	if err != nil || !statusUserPattern.MatchString(username) {
		return "", false
	}
	id, err := url.PathUnescape(parts[2])
	if err != nil || !statusIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}

func noteContainsStatus(text, wantedID string) bool {
	if !statusIDPattern.MatchString(wantedID) {
		return false
	}
	for _, candidate := range candidateURLPattern.FindAllString(text, -1) {
		candidate = strings.TrimRight(candidate, ".,;:!?)]}")
		if id, ok := statusIDFromURL(candidate); ok && id == wantedID {
			return true
		}
	}
	return false
}

func safeHTTPSURL(raw string) string {
	if len(raw) > 4096 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

func safeHTTPURL(raw string) string {
	if len(raw) > 4096 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

func xProfileURL(handle string) string {
	if !statusUserPattern.MatchString(handle) {
		return ""
	}
	return "https://x.com/" + handle
}
