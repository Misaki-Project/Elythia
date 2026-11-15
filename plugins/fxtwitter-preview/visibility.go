// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/elythia-network/elythia/plugin"
)

var (
	errLoginRequired   = errors.New("login required")
	errNoteUnavailable = errors.New("note unavailable")
	errLinkNotInNote   = errors.New("status link is not present in note")
)

func verifyVisibleNote(ctx context.Context, api plugin.API, userID, noteID, postID string) error {
	if userID == "" {
		return errLoginRequired
	}
	if noteID == "" || !statusIDPattern.MatchString(postID) {
		return errLinkNotInNote
	}
	raw, err := api.AsUser(userID).Call(ctx, "notes/show", map[string]any{"noteId": noteID})
	if err != nil {
		return fmt.Errorf("%w: %v", errNoteUnavailable, err)
	}
	var note struct {
		ID   string  `json:"id"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(raw, &note); err != nil || note.ID != noteID || note.Text == nil {
		return errNoteUnavailable
	}
	if !noteContainsStatus(*note.Text, postID) {
		return errLinkNotInNote
	}
	return nil
}
