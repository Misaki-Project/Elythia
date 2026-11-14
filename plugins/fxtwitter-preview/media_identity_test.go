// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodePublicPostAddsOpaqueNativeVideoIdentities(t *testing.T) {
	firstURL := "https://video.twimg.com/amplify_video/a/vid/avc1/320x568/clip.mp4?tag=14&token=first-secret"
	secondURL := "https://video.twimg.com/amplify_video/b/vid/avc1/480x852/clip.mp4?tag=14&token=second-secret"
	post := PublicPost{
		ID:     "1234567890",
		Video:  &PublicVideo{Provider: "x"},
		Videos: []PublicVideoMetadata{{Provider: "x"}, {Provider: "x"}},
		Quote: &PublicQuotedPost{
			ID:     "2234567890",
			Video:  &PublicVideo{Provider: "x"},
			Videos: []PublicVideoMetadata{{Provider: "x"}},
		},
		privateVideoSources: privateVideoSources{
			Version: 1,
			Main:    []privateVideoSource{{URL: firstURL}, {URL: secondURL}},
			Quote:   []privateVideoSource{{URL: secondURL}},
		},
	}
	payload, err := marshalCachedPostPayload(post, true)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePublicPostWithVideoIdentity(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Video == nil || decoded.Video.MediaIdentity == "" || decoded.Video.MediaIdentity != decoded.Videos[0].MediaIdentity {
		t.Fatalf("legacy first video identity = %#v, video list = %#v", decoded.Video, decoded.Videos)
	}
	if decoded.Videos[1].MediaIdentity == "" || decoded.Videos[1].MediaIdentity == decoded.Videos[0].MediaIdentity {
		t.Fatalf("per-video identities are missing or collide: %#v", decoded.Videos)
	}
	if decoded.Quote == nil || decoded.Quote.Video == nil || decoded.Quote.Video.MediaIdentity != decoded.Quote.Videos[0].MediaIdentity || decoded.Quote.Video.MediaIdentity != decoded.Videos[1].MediaIdentity {
		t.Fatalf("quote video identity not attached to its source: %#v", decoded.Quote)
	}
	publicJSON, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "video.twimg.com") || strings.Contains(string(publicJSON), "first-secret") || strings.Contains(string(publicJSON), "second-secret") || strings.Contains(string(publicJSON), "_fxVideoSources") {
		t.Fatalf("public response leaked a private source: %s", publicJSON)
	}
}

func TestVideoMediaIdentityIsStableAndChangesWithFullSourceURL(t *testing.T) {
	base := privateVideoSource{URL: "https://video.twimg.com/amplify_video/a/vid/avc1/320x568/clip.mp4?tag=14&token=first"}
	if got, want := videoMediaIdentity(base), videoMediaIdentity(base); got == "" || got != want {
		t.Fatalf("identity is not deterministic: got %q want %q", got, want)
	}
	rotated := privateVideoSource{URL: "https://video.twimg.com/amplify_video/a/vid/avc1/320x568/clip.mp4?tag=14&token=rotated"}
	if videoMediaIdentity(base) == videoMediaIdentity(rotated) {
		t.Fatal("identity did not change when the full source URL changed")
	}
	if got := videoMediaIdentity(privateVideoSource{URL: "https://video.twimg.com/playlist.m3u8"}); got != "" {
		t.Fatalf("unsafe source received an identity: %q", got)
	}
}

func TestApplyVideoMediaIdentitiesFailsClosedWithoutMatchingVersion(t *testing.T) {
	post := PublicPost{Video: &PublicVideo{Provider: "x", MediaIdentity: strings.Repeat("a", 64)}, Videos: []PublicVideoMetadata{{Provider: "x", MediaIdentity: strings.Repeat("a", 64)}}}
	sources := privateVideoSources{Version: 1, Main: []privateVideoSource{{URL: "https://video.twimg.com/a.mp4"}}}
	applyVideoMediaIdentities(&post, 0, sources)
	if post.Videos[0].MediaIdentity != "" || post.Video.MediaIdentity != "" {
		t.Fatalf("unversioned cached identity was retained: %#v", post)
	}
	applyVideoMediaIdentities(&post, 1, privateVideoSources{Version: 1})
	if post.Videos[0].MediaIdentity != "" || post.Video.MediaIdentity != "" {
		t.Fatalf("identity without a matching source slot was retained: %#v", post)
	}
}

func TestDecodePublicPostIgnoresMalformedOptionalPrivateVideoIdentity(t *testing.T) {
	post := PublicPost{
		ID:     "1234567890",
		Video:  &PublicVideo{Provider: "x", MediaIdentity: strings.Repeat("a", 64)},
		Videos: []PublicVideoMetadata{{Provider: "x", MediaIdentity: strings.Repeat("a", 64)}},
	}
	publicPayload, err := json.Marshal(post)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(publicPayload, &fields); err != nil {
		t.Fatal(err)
	}
	fields["_fxVideoSourcesVersion"] = json.RawMessage(`"future"`)
	fields["_fxVideoSources"] = json.RawMessage(`[]`)
	malformedPayload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePublicPostWithVideoIdentity(malformedPayload)
	if err != nil {
		t.Fatalf("malformed optional private fields broke the public preview: %v", err)
	}
	if decoded.Video == nil || decoded.Video.MediaIdentity != "" || len(decoded.Videos) != 1 || decoded.Videos[0].MediaIdentity != "" {
		t.Fatalf("preexisting identities survived malformed private fields: %#v", decoded)
	}
}
