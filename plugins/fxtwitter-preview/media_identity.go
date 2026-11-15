// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func decodePublicPostWithVideoIdentity(payload []byte) (*PublicPost, error) {
	var post PublicPost
	if err := json.Unmarshal(payload, &post); err != nil {
		return nil, err
	}
	var cached cachedVideoPost
	if err := json.Unmarshal(payload, &cached); err == nil {
		applyVideoMediaIdentities(&post, cached.VideoSourcesVersion, cached.VideoSources)
	} else {
		applyVideoMediaIdentities(&post, 0, privateVideoSources{})
	}
	return &post, nil
}

func applyVideoMediaIdentities(post *PublicPost, version int, sources privateVideoSources) {
	if post == nil {
		return
	}
	for i := range post.Videos {
		post.Videos[i].MediaIdentity = ""
	}
	if post.Video != nil {
		post.Video.MediaIdentity = ""
	}
	if post.Quote != nil {
		for i := range post.Quote.Videos {
			post.Quote.Videos[i].MediaIdentity = ""
		}
		if post.Quote.Video != nil {
			post.Quote.Video.MediaIdentity = ""
		}
	}
	if version != 1 || sources.Version != 1 {
		return
	}
	applyVideoListIdentity(post.Videos, sources.Main)
	if post.Video != nil && post.Video.Provider == "x" && len(sources.Main) > 0 {
		post.Video.MediaIdentity = videoMediaIdentity(sources.Main[0])
	}
	if post.Quote == nil {
		return
	}
	applyVideoListIdentity(post.Quote.Videos, sources.Quote)
	if post.Quote.Video != nil && post.Quote.Video.Provider == "x" && len(sources.Quote) > 0 {
		post.Quote.Video.MediaIdentity = videoMediaIdentity(sources.Quote[0])
	}
}

func applyVideoListIdentity(videos []PublicVideoMetadata, sources []privateVideoSource) {
	for i := range videos {
		if videos[i].Provider == "x" && i < len(sources) {
			videos[i].MediaIdentity = videoMediaIdentity(sources[i])
		}
	}
}

func videoMediaIdentity(source privateVideoSource) string {
	if source.URL == "" {
		return ""
	}
	u, ok := safeNativeVideoURL(source.URL)
	if !ok {
		return ""
	}
	sum := sha256.Sum256([]byte(u.String()))
	return hex.EncodeToString(sum[:])
}
