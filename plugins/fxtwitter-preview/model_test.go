// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import "testing"

func TestStatusIDFromURL(t *testing.T) {
	for _, raw := range []string{
		"https://x.com/example/status/1234567890",
		"https://www.twitter.com/example/status/1234567890?ref=home",
	} {
		id, ok := statusIDFromURL(raw)
		if !ok || id != "1234567890" {
			t.Fatalf("statusIDFromURL(%q) = %q, %v", raw, id, ok)
		}
	}
	for _, raw := range []string{
		"http://x.com/example/status/1234567890",
		"https://evil.example/example/status/1234567890",
		"https://user@x.com/example/status/1234567890",
		"https://x.com:444/example/status/1234567890",
		"https://x.com/not-valid!/status/1234567890",
		"https://x.com/abcdefghijklmnop/status/1234567890",
		"https://x.com/example/status/not-a-number",
		"https://x.com/example/status/1",
		"https://x.com/example/status/1234567890/extra",
	} {
		if _, ok := statusIDFromURL(raw); ok {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestNoteContainsExactStatus(t *testing.T) {
	text := "see https://x.com/a/status/1234567890 and https://x.com/b/status/9999999999"
	if !noteContainsStatus(text, "1234567890") {
		t.Fatal("exact status was not found")
	}
	if noteContainsStatus(text, "123456789") {
		t.Fatal("partial status id matched")
	}
}
