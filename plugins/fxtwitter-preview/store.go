// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errRateLimited = errors.New("cold fetch rate limit exceeded")

const fetchLeaseDuration = 2 * time.Minute

type store struct {
	db  *sql.DB
	cfg config
}

type lease struct {
	PostID string `json:"postId"`
	Token  string `json:"token"`
}

func newStore(db *sql.DB, cfg config) *store { return &store{db: db, cfg: cfg} }

func (s *store) ReadPublic(ctx context.Context, postID string) (*CacheEntry, error) {
	var entry CacheEntry
	var payload, lastSuccessPayload []byte
	var lastSuccessAt *time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT post_id, state, payload, fetched_at, next_fetch_at,
		       COALESCE(last_error_class, ''), last_success_payload, last_success_at
		FROM public_post_cache WHERE post_id = $1
	`, postID).Scan(&entry.PostID, &entry.State, &payload, &entry.FetchedAt,
		&entry.NextFetchAt, &entry.ErrorClass, &lastSuccessPayload, &lastSuccessAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if entry.State == "unavailable" {
		entry.UnavailableReason = unavailableReason(entry.ErrorClass)
	}
	if len(payload) > 0 {
		var post PublicPost
		if err := json.Unmarshal(payload, &post); err != nil {
			return nil, fmt.Errorf("decode public cache: %w", err)
		}
		entry.Post = &post
	} else if entry.State == "unavailable" && isTransientFailureClass(entry.ErrorClass) && len(lastSuccessPayload) > 0 {
		var post PublicPost
		if err := json.Unmarshal(lastSuccessPayload, &post); err != nil {
			return nil, fmt.Errorf("decode last successful public cache: %w", err)
		}
		entry.State = "stale"
		entry.Stale = true
		entry.Post = &post
		entry.FetchedAt = lastSuccessAt
	}
	return &entry, nil
}

func (s *store) AcquireFetch(ctx context.Context, postID, actorID string, now time.Time) (*lease, error) {
	return s.AcquireFetchForRoles(ctx, postID, actorID, nil, now)
}

func (s *store) AcquireFetchForRoles(ctx context.Context, postID, actorID string, roleIDs []string, now time.Time) (*lease, error) {
	if !statusIDPattern.MatchString(postID) || actorID == "" {
		return nil, errors.New("invalid lease request")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.ensureRateLimitSettingsTx(ctx, tx); err != nil {
		return nil, err
	}
	settings, _, _, err := readRateLimitSettingsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	limits := effectiveBudgetFromSettings(settings, roleIDs)
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	var won string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO public_post_cache
			(post_id, state, next_fetch_at, lease_token, lease_expires_at)
		VALUES ($1, 'pending', $2, $3, $4)
		ON CONFLICT (post_id) DO UPDATE SET
			last_success_payload = CASE WHEN public_post_cache.state <> 'ready' AND COALESCE(public_post_cache.last_error_class, '') NOT IN ('network', 'read', 'timeout', 'upstream_429', 'upstream_5xx') THEN NULL ELSE public_post_cache.last_success_payload END,
			last_success_at = CASE WHEN public_post_cache.state <> 'ready' AND COALESCE(public_post_cache.last_error_class, '') NOT IN ('network', 'read', 'timeout', 'upstream_429', 'upstream_5xx') THEN NULL ELSE public_post_cache.last_success_at END,
			state = 'pending',
			payload = NULL,
			next_fetch_at = EXCLUDED.next_fetch_at,
			lease_token = EXCLUDED.lease_token,
			lease_expires_at = EXCLUDED.lease_expires_at,
			updated_at = $5
		WHERE public_post_cache.next_fetch_at <= $5
		  AND (public_post_cache.lease_expires_at IS NULL OR public_post_cache.lease_expires_at <= $5)
		RETURNING lease_token
	`, postID, now.Add(s.cfg.RefetchInterval), token, now.Add(fetchLeaseDuration), now).Scan(&won)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	utcNow := now.UTC()
	hourBucket := utcNow.Truncate(time.Hour)
	dayBucket := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	if ok, err := incrementBudget(ctx, tx, "user-hour:"+actorID, hourBucket, limits.PerHour); err != nil {
		return nil, err
	} else if !ok {
		return nil, errRateLimited
	}
	if ok, err := incrementBudget(ctx, tx, "user-day:"+actorID, dayBucket, limits.PerDay); err != nil {
		return nil, err
	} else if !ok {
		return nil, errRateLimited
	}
	if ok, err := incrementBudget(ctx, tx, "global", hourBucket, settings.GlobalPerHour); err != nil {
		return nil, err
	} else if !ok {
		return nil, errRateLimited
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &lease{PostID: postID, Token: won}, nil
}

func incrementBudget(ctx context.Context, tx *sql.Tx, scope string, bucket time.Time, limit int) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `
		INSERT INTO cold_fetch_budget (scope, bucket_start, count) VALUES ($1, $2, 1)
		ON CONFLICT (scope, bucket_start) DO UPDATE SET count = cold_fetch_budget.count + 1
		WHERE cold_fetch_budget.count < $3
		RETURNING count
	`, scope, bucket, limit).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ClaimFetch rotates the queued lease token immediately before outbound I/O.
// Only one delivery of a duplicated queue payload can win this update, so a
// stale or redelivered job cannot cause another Fx request in the interval.
func (s *store) ClaimFetch(ctx context.Context, queued lease, now time.Time) (*lease, error) {
	claimed, err := randomToken()
	if err != nil {
		return nil, err
	}
	var token string
	err = s.db.QueryRowContext(ctx, `
		UPDATE public_post_cache SET
			lease_token = $1,
			lease_expires_at = $2,
			updated_at = $3
		WHERE post_id = $4 AND lease_token = $5 AND lease_expires_at > $3
		RETURNING lease_token
	`, claimed, now.Add(fetchLeaseDuration), now, queued.PostID, queued.Token).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lease{PostID: queued.PostID, Token: token}, nil
}

func (s *store) CompleteReady(ctx context.Context, l lease, post PublicPost, now time.Time) (bool, error) {
	if post.ID != l.PostID {
		return false, errors.New("lease and post id do not match")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck
	var nextFetchAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT next_fetch_at FROM public_post_cache
		WHERE post_id = $1 AND lease_token = $2 FOR UPDATE
	`, l.PostID, l.Token).Scan(&nextFetchAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if post.Video != nil && post.Video.PosterURL != "" {
		expires := nextFetchAt
		post.Video.PosterExpiresAt = &expires
	}
	if post.Quote != nil && post.Quote.Video != nil && post.Quote.Video.PosterURL != "" {
		expires := nextFetchAt
		post.Quote.Video.PosterExpiresAt = &expires
	}
	var embedToken any
	var embedExpires any
	if s.cfg.VideoPlaybackMode == "officialEmbed" && (playableVideo(post.Video) || (post.Quote != nil && playableVideo(post.Quote.Video))) {
		token, err := randomToken()
		if err != nil {
			return false, err
		}
		expires := nextFetchAt
		if playableVideo(post.Video) {
			post.Video.EmbedToken = token
			post.Video.EmbedExpiresAt = &expires
		}
		if post.Quote != nil && playableVideo(post.Quote.Video) {
			post.Quote.Video.EmbedToken = token
			post.Quote.Video.EmbedExpiresAt = &expires
		}
		embedToken, embedExpires = token, expires
	}
	payload, err := json.Marshal(post)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256([]byte(post.Text))
	res, err := tx.ExecContext(ctx, `
		UPDATE public_post_cache SET state = 'ready', payload = $1, fetched_at = $2,
			lease_token = NULL, lease_expires_at = NULL, last_error_class = NULL,
			embed_token = $5, embed_expires_at = $6, updated_at = $2,
			last_success_payload = $1, last_success_at = $2
		WHERE post_id = $3 AND lease_token = $4
	`, payload, now, l.PostID, l.Token, embedToken, embedExpires)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if err := upsertNormalizedPost(ctx, tx, post, now); err != nil {
		return false, err
	}
	authorSnapshot, err := json.Marshal(post.Author)
	if err != nil {
		return false, err
	}
	var quoteAuthorSnapshot any
	if post.Quote != nil {
		value, err := json.Marshal(post.Quote.Author)
		if err != nil {
			return false, err
		}
		quoteAuthorSnapshot = value
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_body_snapshot
			(post_id, body, content_hash, observed_at, author_snapshot, quote_author_snapshot)
		SELECT $1, $2, $3, $4, $5, $6
		WHERE (SELECT content_hash FROM audit_body_snapshot
		       WHERE post_id = $1 ORDER BY observed_at DESC, id DESC LIMIT 1)
		      IS DISTINCT FROM $3
	`, post.ID, post.Text, hex.EncodeToString(hash[:]), now, authorSnapshot, quoteAuthorSnapshot)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func upsertNormalizedPost(ctx context.Context, tx *sql.Tx, post PublicPost, observedAt time.Time) error {
	var quotePostID any
	if post.Quote != nil && statusIDPattern.MatchString(post.Quote.ID) && validStableAuthor(post.Quote.Author) {
		if err := upsertXUser(ctx, tx, post.Quote.Author, observedAt); err != nil {
			return err
		}
		payload, err := normalizedPostPayload(post.Quote)
		if err != nil {
			return err
		}
		if err := upsertXPost(ctx, tx, post.Quote.ID, post.Quote.Author.ID, payload, nil, observedAt); err != nil {
			return err
		}
		quotePostID = post.Quote.ID
	}
	if !statusIDPattern.MatchString(post.ID) || !validStableAuthor(post.Author) {
		return nil
	}
	if err := upsertXUser(ctx, tx, post.Author, observedAt); err != nil {
		return err
	}
	payload, err := normalizedPostPayload(post)
	if err != nil {
		return err
	}
	return upsertXPost(ctx, tx, post.ID, post.Author.ID, payload, quotePostID, observedAt)
}

func validStableAuthor(author PublicAuthor) bool {
	return xUserIDPattern.MatchString(author.ID) && author.Name != "" && author.ScreenName != ""
}

func upsertXUser(ctx context.Context, tx *sql.Tx, author PublicAuthor, observedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO x_user
			(user_id, name, screen_name, avatar_url, profile_observed_at, updated_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			name = EXCLUDED.name, screen_name = EXCLUDED.screen_name,
			avatar_url = EXCLUDED.avatar_url,
			profile_observed_at = EXCLUDED.profile_observed_at,
			updated_at = EXCLUDED.updated_at
		WHERE x_user.profile_observed_at <= EXCLUDED.profile_observed_at
	`, author.ID, author.Name, author.ScreenName, author.AvatarURL, observedAt)
	return err
}

func upsertXPost(ctx context.Context, tx *sql.Tx, postID, authorID string, payload []byte, quotePostID any, observedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO x_post
			(post_id, author_id, payload, quote_post_id, observed_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (post_id) DO UPDATE SET
			author_id = EXCLUDED.author_id, payload = EXCLUDED.payload,
			quote_post_id = EXCLUDED.quote_post_id,
			observed_at = EXCLUDED.observed_at, updated_at = EXCLUDED.updated_at
		WHERE x_post.observed_at <= EXCLUDED.observed_at
	`, postID, authorID, payload, quotePostID, observedAt)
	return err
}

func normalizedPostPayload(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	delete(payload, "author")
	delete(payload, "quote")
	if video, ok := payload["video"].(map[string]any); ok {
		delete(video, "embedToken")
		delete(video, "embedExpiresAt")
	}
	return json.Marshal(payload)
}

func isTransientFailureClass(class string) bool {
	switch class {
	case "network", "read", "timeout", "upstream_429", "upstream_5xx":
		return true
	default:
		return false
	}
}

func (s *store) CompleteUnavailable(ctx context.Context, l lease, class string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE public_post_cache SET state = 'unavailable', payload = NULL, fetched_at = $1,
			lease_token = NULL, lease_expires_at = NULL, last_error_class = $2, updated_at = $1
			, embed_token = NULL, embed_expires_at = NULL
			, last_success_payload = CASE WHEN $5 THEN last_success_payload ELSE NULL END
			, last_success_at = CASE WHEN $5 THEN last_success_at ELSE NULL END
		WHERE post_id = $3 AND lease_token = $4
	`, now, class, l.PostID, l.Token, isTransientFailureClass(class))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

type AuditSnapshot struct {
	BodyFormat  string    `json:"bodyFormat"`
	ID          int64     `json:"id"`
	PostID      string    `json:"postId"`
	Body        string    `json:"body"`
	ContentHash string    `json:"contentHash"`
	ObservedAt  time.Time `json:"observedAt"`
}

func (s *store) ListAudit(ctx context.Context, postID string) ([]AuditSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, post_id, body, content_hash, observed_at FROM audit_body_snapshot
		WHERE post_id = $1 ORDER BY observed_at DESC, id DESC LIMIT 100
	`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	items := []AuditSnapshot{}
	for rows.Next() {
		var item AuditSnapshot
		if err := rows.Scan(&item.ID, &item.PostID, &item.Body, &item.ContentHash, &item.ObservedAt); err != nil {
			return nil, err
		}
		item.BodyFormat = "normalized_text_v1"
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *store) Delete(ctx context.Context, actor, target, postID string, snapshotID *int64, reason string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	var deleted int64
	if target == "public" || target == "both" {
		res, err := tx.ExecContext(ctx, `DELETE FROM x_post WHERE post_id = $1`, postID)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		deleted += n
		res, err = tx.ExecContext(ctx, `DELETE FROM public_post_cache WHERE post_id = $1`, postID)
		if err != nil {
			return 0, err
		}
		n, _ = res.RowsAffected()
		deleted += n
	}
	if target == "audit" || target == "both" {
		var res sql.Result
		if snapshotID != nil && postID != "" {
			res, err = tx.ExecContext(ctx, `DELETE FROM audit_body_snapshot WHERE id = $1 AND post_id = $2`, *snapshotID, postID)
		} else if snapshotID != nil {
			res, err = tx.ExecContext(ctx, `DELETE FROM audit_body_snapshot WHERE id = $1`, *snapshotID)
		} else {
			res, err = tx.ExecContext(ctx, `DELETE FROM audit_body_snapshot WHERE post_id = $1`, postID)
		}
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		deleted += n
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_delete_log (actor_id, target, post_id, snapshot_id, reason, deleted_count)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, actor, target, postID, snapshotID, reason, deleted)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *store) RetainAudit(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if s.cfg.AuditRetention == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.ExecContext(ctx, `
		WITH doomed AS (
			SELECT id FROM audit_body_snapshot WHERE observed_at < $1 ORDER BY observed_at, id LIMIT $2
		)
		DELETE FROM audit_body_snapshot WHERE id IN (SELECT id FROM doomed)
	`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count > 0 {
		reason := fmt.Sprintf("configured audit retention %s; cutoff %s", s.cfg.AuditRetention, cutoff.UTC().Format(time.RFC3339))
		_, err = tx.ExecContext(ctx, `
			INSERT INTO audit_delete_log (actor_id, target, reason, deleted_count)
			VALUES ('system', 'retention', $1, $2)
		`, reason, count)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
