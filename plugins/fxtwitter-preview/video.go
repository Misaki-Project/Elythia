// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

const (
	nativeVideoMaxBytes        = 32 << 20
	nativeVideoMaxBitrate      = 2_500_000
	nativeVideoUserPerMinute   = 4
	nativeVideoGlobalPerMinute = 12
)

type fxVideo struct {
	Type         string          `json:"type"`
	Format       string          `json:"format"`
	URL          string          `json:"url"`
	ThumbnailURL string          `json:"thumbnail_url"`
	Duration     float64         `json:"duration"`
	Formats      []fxVideoFormat `json:"formats"`
}

type fxVideoFormat struct {
	URL       string `json:"url"`
	Container string `json:"container"`
	Codec     string `json:"codec"`
	Bitrate   int    `json:"bitrate"`
}

type privateVideoSource struct {
	URL string `json:"url,omitempty"`
}

type privateVideoSources struct {
	Version int                  `json:"version"`
	Main    []privateVideoSource `json:"main"`
	Quote   []privateVideoSource `json:"quote"`
}

type cachedVideoPost struct {
	PublicPost
	VideoSourcesVersion int                 `json:"_fxVideoSourcesVersion"`
	VideoSources        privateVideoSources `json:"_fxVideoSources"`
}

func selectVideoSources(videos []fxVideo) []privateVideoSource {
	sources := make([]privateVideoSource, len(videos))
	for i, video := range videos {
		sources[i] = selectVideoSource(video)
	}
	return sources
}

func selectVideoSource(video fxVideo) privateVideoSource {
	candidates := append([]fxVideoFormat(nil), video.Formats...)
	if video.URL != "" {
		candidates = append(candidates, fxVideoFormat{URL: video.URL, Container: "mp4"})
	}
	best := fxVideoFormat{}
	bestScore := -1
	for _, candidate := range candidates {
		container := strings.ToLower(candidate.Container)
		if container != "" && container != "mp4" {
			continue
		}
		if candidate.Bitrate < 0 || candidate.Bitrate > nativeVideoMaxBitrate || (candidate.Codec != "" && !strings.EqualFold(candidate.Codec, "h264") && !strings.EqualFold(candidate.Codec, "avc1")) {
			continue
		}
		if _, ok := safeNativeVideoURL(candidate.URL); !ok {
			continue
		}
		if candidate.Bitrate > 0 && video.Duration > 0 && video.Duration*float64(candidate.Bitrate)/8 > nativeVideoMaxBytes {
			continue
		}
		score := candidate.Bitrate
		if score == 0 {
			score = 1
		}
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	if bestScore < 0 {
		return privateVideoSource{}
	}
	u, _ := safeNativeVideoURL(best.URL)
	return privateVideoSource{URL: u.String()}
}

func safeNativeVideoURL(raw string) (*url.URL, bool) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "video.twimg.com") || !strings.EqualFold(u.Host, "video.twimg.com") || u.User != nil || u.Port() != "" || u.Fragment != "" || !strings.EqualFold(path.Ext(u.Path), ".mp4") {
		return nil, false
	}
	return u, true
}

func marshalCachedPostPayload(post PublicPost, nativeProxy bool) ([]byte, error) {
	raw, err := json.Marshal(post)
	if err != nil || !nativeProxy {
		return raw, err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	version, _ := json.Marshal(post.privateVideoSources.Version)
	sources, err := json.Marshal(post.privateVideoSources)
	if err != nil {
		return nil, err
	}
	payload["_fxVideoSourcesVersion"] = version
	payload["_fxVideoSources"] = sources
	return json.Marshal(payload)
}

func hasVideoSource(sources []privateVideoSource) bool {
	for _, source := range sources {
		if source.URL != "" {
			return true
		}
	}
	return false
}

func setPlaybackAvailability(videos []PublicVideoMetadata, sources []privateVideoSource) {
	for i := range videos {
		videos[i].PlaybackAvailable = i < len(sources) && sources[i].URL != ""
	}
}

func (s *store) NeedsNativeVideoMetadata(ctx context.Context, postID string) (bool, error) {
	if !statusIDPattern.MatchString(postID) {
		return false, errors.New("invalid post id")
	}
	var needs bool
	err := s.db.QueryRowContext(ctx, `
		SELECT state = 'ready'
		   AND (COALESCE(payload->>'hasVideo', 'false') = 'true' OR COALESCE(payload->'quote'->>'hasVideo', 'false') = 'true')
		   AND (COALESCE(payload->'video'->>'provider' = 'x', false)
		        OR COALESCE(payload->'videos'->0->>'provider' = 'x', false)
		        OR COALESCE(payload->'quote'->'video'->>'provider' = 'x', false)
		        OR COALESCE(payload->'quote'->'videos'->0->>'provider' = 'x', false))
		   AND COALESCE(payload->>'_fxVideoSourcesVersion', '') <> '1'
	FROM public_post_cache WHERE post_id = $1
	`, postID).Scan(&needs)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return needs, err
}

func (s *store) ResolveNativeVideoSource(ctx context.Context, postID, token, target string, index int, now time.Time) (string, error) {
	if !statusIDPattern.MatchString(postID) || !embedNoncePattern.MatchString(token) || index < 0 || index >= maxFxMediaItems {
		return "", errors.New("invalid video capability request")
	}
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT payload FROM public_post_cache
		WHERE post_id = $1 AND state = 'ready' AND embed_token = $2
		  AND embed_expires_at > $3 AND next_fetch_at > $3
	`, postID, token, now).Scan(&payload)
	if err != nil {
		return "", errors.New("video capability unavailable")
	}
	var cached cachedVideoPost
	if err := json.Unmarshal(payload, &cached); err != nil || cached.VideoSourcesVersion != 1 || cached.VideoSources.Version != 1 {
		return "", errors.New("video source unavailable")
	}
	var source []privateVideoSource
	var publicAvailable bool
	switch target {
	case "main":
		source = cached.VideoSources.Main
		if len(cached.Videos) > 0 {
			publicAvailable = index < len(cached.Videos) && cached.Videos[index].Provider == "x" && cached.Videos[index].PlaybackAvailable
		} else {
			publicAvailable = index == 0 && cached.Video != nil && cached.Video.Provider == "x" && cached.Video.PlaybackAvailable
		}
	case "quote":
		if cached.Quote == nil {
			return "", errors.New("video target unavailable")
		}
		source = cached.VideoSources.Quote
		if len(cached.Quote.Videos) > 0 {
			publicAvailable = index < len(cached.Quote.Videos) && cached.Quote.Videos[index].Provider == "x" && cached.Quote.Videos[index].PlaybackAvailable
		} else {
			publicAvailable = index == 0 && cached.Quote.Video != nil && cached.Quote.Video.Provider == "x" && cached.Quote.Video.PlaybackAvailable
		}
	default:
		return "", errors.New("invalid video target")
	}
	if index >= len(source) || source[index].URL == "" || !publicAvailable {
		return "", errors.New("video source unavailable")
	}
	u, ok := safeNativeVideoURL(source[index].URL)
	if !ok {
		return "", errors.New("video source unavailable")
	}
	return u.String(), nil
}

func fetchVideoWithAdmission(ctx context.Context, base *http.Client, rawURL string, maxBytes int, admission *mediaAdmission) (plugin.Blob, func(), error) {
	u, ok := safeNativeVideoURL(rawURL)
	if !ok || maxBytes < 12 || maxBytes > nativeVideoMaxBytes {
		return plugin.Blob{}, nil, errors.New("video source is not allowlisted")
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
	if res.StatusCode != http.StatusOK {
		return plugin.Blob{}, nil, fmt.Errorf("video status %d", res.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || contentType != "video/mp4" {
		return plugin.Blob{}, nil, errors.New("video content type is not allowed")
	}
	if res.ContentLength > int64(maxBytes) {
		return plugin.Blob{}, nil, errNativeVideoTooLarge
	}
	body, err := readBoundedMediaBody(res.Body, maxBytes)
	if err != nil {
		if errors.Is(err, errMediaBodyTooLarge) {
			return plugin.Blob{}, nil, errNativeVideoTooLarge
		}
		return plugin.Blob{}, nil, err
	}
	if !validMP4FTYP(body) {
		return plugin.Blob{}, nil, errors.New("video content is not a supported MP4")
	}
	keepReservation = true
	return plugin.Blob{ContentType: "video/mp4", Body: body, CacheControl: "private, no-store", WriteTimeout: mediaResponseWriteTimeout}, release, nil
}

func validMP4FTYP(body []byte) bool {
	if len(body) < 12 || string(body[4:8]) != "ftyp" {
		return false
	}
	boxSize := binary.BigEndian.Uint32(body[:4])
	return boxSize >= 12 && boxSize <= uint32(len(body))
}

type videoProxyLimiter struct {
	mu          sync.Mutex
	users       map[string]videoWindow
	global      videoWindow
	userLimit   int
	globalLimit int
}

type videoWindow struct {
	minute time.Time
	count  int
}

func newVideoProxyLimiter(userLimit, globalLimit int) *videoProxyLimiter {
	return &videoProxyLimiter{users: make(map[string]videoWindow), userLimit: userLimit, globalLimit: globalLimit}
}

func (l *videoProxyLimiter) Allow(userID string, now time.Time) bool {
	if userID == "" || l.userLimit < 1 || l.globalLimit < 1 {
		return false
	}
	minute := now.UTC().Truncate(time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.global.minute.Equal(minute) {
		l.global = videoWindow{minute: minute}
	}
	for user, window := range l.users {
		if window.minute.Before(minute) {
			delete(l.users, user)
		}
	}
	window := l.users[userID]
	if !window.minute.Equal(minute) {
		window = videoWindow{minute: minute}
	}
	if window.count >= l.userLimit || l.global.count >= l.globalLimit {
		return false
	}
	window.count++
	l.users[userID] = window
	l.global.count++
	return true
}

var processNativeVideoAdmission = newMediaAdmission(2, 8, (64<<20)+2)
var processNativeVideoLimiter = newVideoProxyLimiter(nativeVideoUserPerMinute, nativeVideoGlobalPerMinute)
var errNativeVideoTooLarge = errors.New("video response exceeds the 32 MiB playback limit")
