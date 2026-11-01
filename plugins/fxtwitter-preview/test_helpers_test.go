// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"encoding/json"
	"testing"
	"time"
)

func testConfig() config {
	return config{
		ExternalFetchEnabled:     true,
		RefetchInterval:          time.Hour,
		RequestTimeout:           time.Second,
		MaxResponseBytes:         1024,
		AuthenticatedBudget:      100,
		AuthenticatedDailyBudget: 600,
		GlobalBudget:             300,
		VideoPlaybackMode:        "officialEmbed",
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
