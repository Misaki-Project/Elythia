// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"mime"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

const fxEndpointPrefix = "https://api.fxtwitter.com/2/status/"

type fetchError struct {
	class string
	err   error
}

func (e *fetchError) Error() string { return e.class + ": " + e.err.Error() }
func (e *fetchError) Unwrap() error { return e.err }

func errorClass(err error) string {
	var e *fetchError
	if errors.As(err, &e) {
		return e.class
	}
	return "internal"
}

type fxClient struct {
	http *http.Client
	cfg  config
}

func newFxClient(base *http.Client, cfg config) *fxClient {
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &fxClient{http: &client, cfg: cfg}
}

func (c *fxClient) Fetch(ctx context.Context, postID string) (PublicPost, error) {
	if !statusIDPattern.MatchString(postID) {
		return PublicPost{}, &fetchError{"invalid_id", errors.New("post id must contain 2-20 digits")}
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fxEndpointPrefix+postID, nil)
	if err != nil {
		return PublicPost{}, &fetchError{"request", err}
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return PublicPost{}, &fetchError{"network", err}
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 || (res.StatusCode >= 300 && res.StatusCode < 400) {
		class := "upstream_status"
		switch {
		case res.StatusCode == http.StatusTooManyRequests:
			class = "upstream_429"
		case res.StatusCode >= 500:
			class = "upstream_5xx"
		}
		return PublicPost{}, &fetchError{class, fmt.Errorf("status %d", res.StatusCode)}
	}
	mediaType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return PublicPost{}, &fetchError{"content_type", fmt.Errorf("unexpected content type %q", res.Header.Get("Content-Type"))}
	}
	limited := io.LimitReader(res.Body, int64(c.cfg.MaxResponseBytes)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return PublicPost{}, &fetchError{"read", err}
	}
	if len(body) > c.cfg.MaxResponseBytes {
		return PublicPost{}, &fetchError{"response_too_large", errors.New("response exceeds configured limit")}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if class, ok := rootTombstoneClass(body, postID); ok {
			return PublicPost{}, &fetchError{class, errors.New("Fx status unavailable")}
		}
		return PublicPost{}, &fetchError{"upstream_status", fmt.Errorf("status %d", res.StatusCode)}
	}
	return parseFxResponse(body, postID)
}

type fxTombstone struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
	ID       string `json:"id"`
}

func tombstoneReason(raw []byte, wantedID string) (string, bool) {
	var value fxTombstone
	if err := json.Unmarshal(raw, &value); err != nil || value.Type != "tombstone" {
		return "", false
	}
	if value.Provider != "twitter" || (value.ID != "" && (!statusIDPattern.MatchString(value.ID) || (wantedID != "" && value.ID != wantedID))) {
		return "unknown", true
	}
	switch value.Reason {
	case "deleted", "suspended", "private", "blocked", "unavailable":
		return value.Reason, true
	default:
		return "unknown", true
	}
}

func rootTombstoneClass(body []byte, wantedID string) (string, bool) {
	var envelope fxResponse
	if json.Unmarshal(body, &envelope) != nil || (envelope.Code != http.StatusOK && (envelope.Code < 400 || envelope.Code >= 500 || envelope.Code == http.StatusTooManyRequests)) {
		return "", false
	}
	reason, ok := tombstoneReason(envelope.Status, wantedID)
	return "tombstone_" + reason, ok
}

func unavailableReason(class string) string {
	switch class {
	case "tombstone_deleted":
		return "deleted"
	case "tombstone_suspended":
		return "suspended"
	case "tombstone_private":
		return "private"
	case "tombstone_blocked":
		return "blocked"
	case "tombstone_unavailable":
		return "unavailable"
	}
	if isTransientFailureClass(class) {
		return "fetch_failed"
	}
	return "unknown"
}

type fxResponse struct {
	Code   int             `json:"code"`
	Status json.RawMessage `json:"status"`
}

type fxStatus struct {
	Type              string `json:"type"`
	ID                string `json:"id"`
	Text              string `json:"text"`
	CreatedAt         string `json:"created_at"`
	CreatedTimestamp  int64  `json:"created_timestamp"`
	Likes             int64  `json:"likes"`
	Reposts           int64  `json:"reposts"`
	Quotes            int64  `json:"quotes"`
	Replies           int64  `json:"replies"`
	PossiblySensitive *bool  `json:"possibly_sensitive"`
	Author            struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		ScreenName string `json:"screen_name"`
		AvatarURL  string `json:"avatar_url"`
	} `json:"author"`
	Media struct {
		External *struct {
			Type         string `json:"type"`
			URL          string `json:"url"`
			ThumbnailURL string `json:"thumbnail_url"`
		} `json:"external"`
		Photos []struct {
			URL     string `json:"url"`
			Width   int    `json:"width"`
			Height  int    `json:"height"`
			AltText string `json:"altText"`
		} `json:"photos"`
		Videos []struct {
			ThumbnailURL string  `json:"thumbnail_url"`
			Duration     float64 `json:"duration"`
		} `json:"videos"`
	} `json:"media"`
	RawText struct {
		Text   string `json:"text"`
		Facets []struct {
			Type        string `json:"type"`
			Indices     []int  `json:"indices"`
			Original    string `json:"original"`
			Replacement string `json:"replacement"`
			Display     string `json:"display"`
		} `json:"facets"`
	} `json:"raw_text"`
	Quote json.RawMessage `json:"quote"`
}

const (
	maxFxTextBytes            = 64 << 10
	maxFxAuthorNameBytes      = 512
	maxFxScreenNameBytes      = 64
	maxFxAltTextBytes         = 4 << 10
	maxFxMediaItems           = 4
	maxFxTextFacets           = 64
	maxFxDimension            = 100_000
	maxFxVideoDurationSeconds = float64(24 * 60 * 60)
)

func parseFxResponse(body []byte, wantedID string) (PublicPost, error) {
	if class, ok := rootTombstoneClass(body, wantedID); ok {
		return PublicPost{}, &fetchError{class, errors.New("Fx status unavailable")}
	}
	var envelope fxResponse
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code != http.StatusOK || len(envelope.Status) == 0 {
		return PublicPost{}, &fetchError{"schema", errors.New("invalid Fx response envelope")}
	}
	var status fxStatus
	if err := json.Unmarshal(envelope.Status, &status); err != nil || status.ID != wantedID || status.Author.Name == "" || status.Author.ScreenName == "" {
		return PublicPost{}, &fetchError{"schema", errors.New("invalid or non-status Fx payload")}
	}
	if err := validateFxStatus(status); err != nil {
		return PublicPost{}, &fetchError{"schema", err}
	}
	text, textEntities := normalizedFxText(status)
	post := PublicPost{
		ID:           wantedID,
		URL:          "https://x.com/i/status/" + wantedID,
		Text:         text,
		TextEntities: textEntities,
		CreatedAt:    normalizedFxCreatedAt(status),
		Author:       PublicAuthor{ID: normalizedXUserID(status.Author.ID), Name: status.Author.Name, ScreenName: status.Author.ScreenName, AvatarURL: safeHTTPSURL(status.Author.AvatarURL), ProfileURL: xProfileURL(status.Author.ScreenName)},
		Likes:        max64(status.Likes, 0), Reposts: max64(status.Reposts, 0), Quotes: max64(status.Quotes, 0), Replies: max64(status.Replies, 0),
		IsSensitive: status.PossiblySensitive,
		Images:      []PublicImage{},
	}
	fillPublicMedia(&post.CreatedAt, &post.Images, &post.HasVideo, &post.Video, status)
	if len(status.Quote) > 0 && string(status.Quote) != "null" {
		var discriminator struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(status.Quote, &discriminator); err != nil {
			return PublicPost{}, &fetchError{"schema", errors.New("invalid Fx quoted status")}
		}
		if discriminator.Type == "tombstone" {
			post.QuoteUnavailableReason = "unknown"
			if reason, ok := tombstoneReason(status.Quote, ""); ok {
				post.QuoteUnavailableReason = reason
			}
		} else {
			var quote fxStatus
			if err := json.Unmarshal(status.Quote, &quote); err != nil || !statusIDPattern.MatchString(quote.ID) || quote.Author.Name == "" || quote.Author.ScreenName == "" {
				return PublicPost{}, &fetchError{"schema", errors.New("invalid Fx quoted status")}
			}
			if err := validateFxStatus(quote); err != nil {
				return PublicPost{}, &fetchError{"schema", err}
			}
			quoteText, quoteTextEntities := normalizedFxText(quote)
			publicQuote := &PublicQuotedPost{
				ID: quote.ID, URL: "https://x.com/i/status/" + quote.ID, Text: quoteText, TextEntities: quoteTextEntities, CreatedAt: normalizedFxCreatedAt(quote),
				Author:      PublicAuthor{ID: normalizedXUserID(quote.Author.ID), Name: quote.Author.Name, ScreenName: quote.Author.ScreenName, AvatarURL: safeHTTPSURL(quote.Author.AvatarURL), ProfileURL: xProfileURL(quote.Author.ScreenName)},
				IsSensitive: quote.PossiblySensitive,
				Images:      []PublicImage{},
			}
			fillPublicMedia(&publicQuote.CreatedAt, &publicQuote.Images, &publicQuote.HasVideo, &publicQuote.Video, quote)
			post.Quote = publicQuote
		}
	}
	return post, nil
}

func normalizedXUserID(value string) string {
	if xUserIDPattern.MatchString(value) {
		return value
	}
	return ""
}

func normalizedFxCreatedAt(status fxStatus) string {
	if status.CreatedTimestamp > 0 {
		return time.Unix(status.CreatedTimestamp, 0).UTC().Format(time.RFC3339)
	}
	if parsed, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", status.CreatedAt); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return status.CreatedAt
}

func normalizedFxText(status fxStatus) (string, []PublicTextEntity) {
	if status.RawText.Text == "" {
		return html.UnescapeString(status.Text), nil
	}
	text := status.RawText.Text
	encoded := utf16.Encode([]rune(text))
	facets := append([]struct {
		Type        string `json:"type"`
		Indices     []int  `json:"indices"`
		Original    string `json:"original"`
		Replacement string `json:"replacement"`
		Display     string `json:"display"`
	}(nil), status.RawText.Facets...)
	sort.SliceStable(facets, func(i, j int) bool {
		if len(facets[i].Indices) != 2 || len(facets[j].Indices) != 2 {
			return len(facets[i].Indices) == 2
		}
		return facets[i].Indices[0] < facets[j].Indices[0]
	})
	output := make([]uint16, 0, len(encoded))
	entities := make([]PublicTextEntity, 0, len(facets))
	// Facet offsets and originals belong to the raw UTF-16 text. Match them
	// before decoding, then decode each visible output fragment exactly once.
	appendVisible := func(value string) {
		output = append(output, utf16.Encode([]rune(html.UnescapeString(value)))...)
	}
	appendVisibleUnits := func(value []uint16) {
		appendVisible(string(utf16.Decode(value)))
	}
	cursor := 0
	for _, facet := range facets {
		if len(facet.Indices) != 2 {
			continue
		}
		start, end := facet.Indices[0], facet.Indices[1]
		if start < cursor || start < 0 || end <= start || end > len(encoded) {
			continue
		}
		original := string(utf16.Decode(encoded[start:end]))
		if facet.Original != "" && facet.Original != original {
			continue
		}
		appendVisibleUnits(encoded[cursor:start])
		visibleOriginal := html.UnescapeString(original)
		entity := PublicTextEntity{Kind: facet.Type, Start: len(output), Display: visibleOriginal}
		switch facet.Type {
		case "url":
			entity.Href = safeHTTPURL(html.UnescapeString(facet.Replacement))
			if entity.Href == "" {
				appendVisible(original)
				cursor = end
				continue
			}
			entity.Display = entity.Href
			output = append(output, utf16.Encode([]rune(entity.Href))...)
		case "media":
			cursor = end
			if cursor < len(encoded) && len(output) > 0 && output[len(output)-1] == ' ' && encoded[cursor] == ' ' {
				cursor++
			} else if cursor == len(encoded) && len(output) > 0 && output[len(output)-1] == ' ' {
				output = output[:len(output)-1]
			}
			continue
		case "hashtag":
			entity.Tag = strings.TrimPrefix(strings.TrimPrefix(visibleOriginal, "#"), "＃")
			if entity.Tag == "" || len(entity.Tag) > 256 || strings.IndexFunc(entity.Tag, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				appendVisible(original)
				cursor = end
				continue
			}
			appendVisible(original)
		case "mention":
			handle := strings.TrimPrefix(strings.TrimPrefix(visibleOriginal, "@"), "＠")
			entity.Href = xProfileURL(handle)
			if entity.Href == "" {
				appendVisible(original)
				cursor = end
				continue
			}
			appendVisible(original)
		default:
			appendVisible(original)
			cursor = end
			continue
		}
		entity.End = len(output)
		entities = append(entities, entity)
		cursor = end
	}
	appendVisibleUnits(encoded[cursor:])
	return string(utf16.Decode(output)), entities
}

func fillPublicMedia(createdAt *string, images *[]PublicImage, hasVideo *bool, publicVideo **PublicVideo, status fxStatus) {
	for _, photo := range status.Media.Photos {
		if u := safeHTTPSURL(photo.URL); u != "" {
			*images = append(*images, PublicImage{URL: u, Width: positive(photo.Width), Height: positive(photo.Height), Alt: photo.AltText})
		}
	}
	if len(status.Media.Videos) > 0 {
		*hasVideo = true
		video := status.Media.Videos[0]
		*publicVideo = &PublicVideo{Provider: "x", PosterURL: safeHTTPSURL(video.ThumbnailURL), DurationMS: int64(math.Round(video.Duration * 1000))}
	} else if status.Media.External != nil && status.Media.External.Type == "video" {
		*hasVideo = true
		video := &PublicVideo{Provider: "external", PosterURL: safeHTTPSURL(status.Media.External.ThumbnailURL)}
		*publicVideo = video
	}
}

func validateFxStatus(status fxStatus) error {
	if len(status.Text) > maxFxTextBytes || len(status.RawText.Text) > maxFxTextBytes || len(status.RawText.Facets) > maxFxTextFacets || len(status.Author.Name) > maxFxAuthorNameBytes ||
		len(status.Author.ScreenName) > maxFxScreenNameBytes || len(status.CreatedAt) > 128 {
		return errors.New("Fx text field exceeds limit")
	}
	for _, facet := range status.RawText.Facets {
		if len(facet.Type) > 32 || len(facet.Indices) > 2 || len(facet.Original) > 4096 || len(facet.Replacement) > 4096 || len(facet.Display) > 2048 {
			return errors.New("Fx text facet exceeds limit")
		}
	}
	if len(status.Media.Photos) > maxFxMediaItems || len(status.Media.Videos) > maxFxMediaItems {
		return errors.New("Fx media collection exceeds limit")
	}
	if status.Media.External != nil && (len(status.Media.External.Type) > 32 || len(status.Media.External.URL) > 4096 || len(status.Media.External.ThumbnailURL) > 4096) {
		return errors.New("Fx external media field exceeds limit")
	}
	for _, photo := range status.Media.Photos {
		if len(photo.AltText) > maxFxAltTextBytes || len(photo.URL) > 4096 ||
			photo.Width < 0 || photo.Width > maxFxDimension || photo.Height < 0 || photo.Height > maxFxDimension {
			return errors.New("Fx photo field exceeds limit")
		}
	}
	for _, video := range status.Media.Videos {
		if len(video.ThumbnailURL) > 4096 || math.IsNaN(video.Duration) || math.IsInf(video.Duration, 0) ||
			video.Duration < 0 || video.Duration > maxFxVideoDurationSeconds {
			return errors.New("Fx video field exceeds limit")
		}
	}
	return nil
}

func positive(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
