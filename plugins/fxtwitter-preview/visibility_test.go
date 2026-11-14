// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/elythia-network/elythia/plugin"
)

type stubAPI struct {
	actor    string
	endpoint string
	params   map[string]any
	body     string
	err      error
}

func (a *stubAPI) Anonymous() plugin.Caller { return &stubCaller{api: a} }
func (a *stubAPI) AsUser(actor string) plugin.Caller {
	a.actor = actor
	return &stubCaller{api: a}
}

type stubCaller struct{ api *stubAPI }

func (c *stubCaller) Call(_ context.Context, endpoint string, params any) (json.RawMessage, error) {
	c.api.endpoint = endpoint
	c.api.params, _ = params.(map[string]any)
	return json.RawMessage(c.api.body), c.api.err
}

func TestVerifyVisibleNoteRequiresUserAndExactLink(t *testing.T) {
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/alice/status/1234567890"}`}
	if err := verifyVisibleNote(context.Background(), a, "", "note1", "1234567890"); !errors.Is(err, errLoginRequired) {
		t.Fatalf("anonymous error = %v", err)
	}
	if err := verifyVisibleNote(context.Background(), a, "user1", "note1", "1234567890"); err != nil {
		t.Fatal(err)
	}
	if a.actor != "user1" || a.endpoint != "notes/show" || a.params["noteId"] != "note1" {
		t.Fatalf("native API call = actor:%q endpoint:%q params:%v", a.actor, a.endpoint, a.params)
	}

	a.body = `{"id":"note1","text":"https://x.com/alice/status/9999999999"}`
	if err := verifyVisibleNote(context.Background(), a, "user1", "note1", "1234567890"); !errors.Is(err, errLinkNotInNote) {
		t.Fatalf("wrong-link error = %v", err)
	}
	a.err = errors.New("not visible")
	if err := verifyVisibleNote(context.Background(), a, "user1", "note1", "1234567890"); !errors.Is(err, errNoteUnavailable) {
		t.Fatalf("inaccessible-note error = %v", err)
	}
}
