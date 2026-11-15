// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

func jobs(ctx plugin.Context, j plugin.Jobs) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	s := newStore(ctx.Storage().DB(), cfg)
	client := newFxClient(ctx.HTTP(), cfg)
	j.Handle("fetch-post", func(jobCtx context.Context, raw json.RawMessage) error {
		var l lease
		if err := json.Unmarshal(raw, &l); err != nil || !statusIDPattern.MatchString(l.PostID) || l.Token == "" {
			return errors.New("invalid fetch job payload")
		}
		if !cfg.ExternalFetchEnabled {
			_, err := s.CompleteUnavailable(jobCtx, l, "disabled", time.Now().UTC())
			return err
		}
		claimed, err := s.ClaimFetch(jobCtx, l, time.Now().UTC())
		if err != nil {
			return err
		}
		if claimed == nil {
			ctx.Logger().Info("ignored stale Fx fetch job", "postId", l.PostID)
			return nil
		}
		fetchCtx, cancelFetch := context.WithTimeout(context.WithoutCancel(jobCtx), cfg.RequestTimeout)
		post, fetchErr := client.Fetch(fetchCtx, claimed.PostID)
		cancelFetch()
		now := time.Now().UTC()
		completeCtx, cancelComplete := context.WithTimeout(context.WithoutCancel(jobCtx), cfg.RequestTimeout)
		defer cancelComplete()
		if fetchErr != nil {
			if _, err := s.CompleteUnavailable(completeCtx, *claimed, errorClass(fetchErr), now); err != nil {
				return err
			}
			ctx.Logger().Warn("Fx post unavailable", "postId", claimed.PostID, "class", errorClass(fetchErr))
			return nil
		}
		updated, err := s.CompleteReady(completeCtx, *claimed, post, now)
		if err != nil {
			return err
		}
		if !updated {
			ctx.Logger().Info("ignored stale Fx fetch completion", "postId", l.PostID)
		}
		return nil
	})
	j.Handle("audit-retention", func(jobCtx context.Context, _ json.RawMessage) error {
		if cfg.AuditRetention == 0 {
			return nil
		}
		deleted, err := s.RetainAudit(jobCtx, time.Now().UTC().Add(-cfg.AuditRetention), 1000)
		if err != nil {
			return err
		}
		if deleted > 0 {
			ctx.Logger().Info("expired Fx audit snapshots removed", "count", deleted)
		}
		return nil
	})
	j.Schedule("17 3 * * *", "audit-retention", nil)
	return nil
}
