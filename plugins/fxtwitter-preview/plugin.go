// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import "github.com/elythia-network/elythia/plugin"

var Plugin = plugin.Definition{
	Name:       "fxtwitter-preview",
	Version:    "0.4.0",
	APIVersion: plugin.APIVersion,
	Migrations: migrations,
	Routes:     routes,
	Jobs:       jobs,
}

var migrations = []plugin.Migration{{Version: 1, SQL: `
	CREATE TABLE public_post_cache (
		post_id text PRIMARY KEY CHECK (post_id ~ '^[0-9]{2,20}$'),
		state text NOT NULL CHECK (state IN ('pending', 'ready', 'unavailable')),
		payload jsonb,
		fetched_at timestamptz,
		next_fetch_at timestamptz NOT NULL,
		lease_token text,
		lease_expires_at timestamptz,
		last_error_class text,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		CHECK ((state = 'ready') = (payload IS NOT NULL))
	);
	CREATE INDEX public_post_cache_due_idx ON public_post_cache (next_fetch_at);

	CREATE TABLE audit_body_snapshot (
		id bigserial PRIMARY KEY,
		post_id text NOT NULL CHECK (post_id ~ '^[0-9]{2,20}$'),
		body text NOT NULL,
		content_hash text NOT NULL,
		observed_at timestamptz NOT NULL DEFAULT now()
	);
	CREATE INDEX audit_body_snapshot_post_observed_idx ON audit_body_snapshot (post_id, observed_at DESC);
	CREATE INDEX audit_body_snapshot_retention_idx ON audit_body_snapshot (observed_at, id);
	CREATE INDEX audit_body_snapshot_post_hash_idx ON audit_body_snapshot (post_id, content_hash);

	CREATE TABLE audit_delete_log (
		id bigserial PRIMARY KEY,
		actor_id text NOT NULL,
		target text NOT NULL CHECK (target IN ('public', 'audit', 'both', 'retention')),
		post_id text,
		snapshot_id bigint,
		reason text NOT NULL,
		deleted_count bigint NOT NULL CHECK (deleted_count >= 0),
		created_at timestamptz NOT NULL DEFAULT now()
	);

	CREATE TABLE cold_fetch_budget (
		scope text NOT NULL,
		bucket_start timestamptz NOT NULL,
		count integer NOT NULL CHECK (count >= 0),
		PRIMARY KEY (scope, bucket_start)
	);
`}, {Version: 2, SQL: `
	ALTER TABLE public_post_cache ADD COLUMN embed_token text;
	ALTER TABLE public_post_cache ADD COLUMN embed_expires_at timestamptz;
	CREATE UNIQUE INDEX public_post_cache_embed_token_idx ON public_post_cache (embed_token) WHERE embed_token IS NOT NULL;
`}, {Version: 3, SQL: `
	ALTER TABLE public_post_cache ADD COLUMN last_success_payload jsonb;
	ALTER TABLE public_post_cache ADD COLUMN last_success_at timestamptz;
	UPDATE public_post_cache
	SET last_success_payload = payload, last_success_at = fetched_at
	WHERE state = 'ready' AND payload IS NOT NULL;
`}, {Version: 4, SQL: `
	CREATE TABLE x_user (
		user_id text PRIMARY KEY CHECK (user_id ~ '^[0-9]{1,20}$'),
		name text NOT NULL,
		screen_name text NOT NULL,
		avatar_url text,
		profile_observed_at timestamptz NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now()
	);

	CREATE TABLE x_post (
		post_id text PRIMARY KEY CHECK (post_id ~ '^[0-9]{2,20}$'),
		author_id text NOT NULL,
		payload jsonb NOT NULL CHECK (NOT (payload ? 'author') AND NOT (payload ? 'quote')),
		quote_post_id text,
		observed_at timestamptz NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		FOREIGN KEY (author_id) REFERENCES x_user(user_id) ON UPDATE CASCADE ON DELETE RESTRICT,
		FOREIGN KEY (quote_post_id) REFERENCES x_post(post_id) ON UPDATE CASCADE ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED
	);
	CREATE INDEX x_post_author_observed_idx ON x_post (author_id, observed_at DESC);
	CREATE INDEX x_post_quote_idx ON x_post (quote_post_id) WHERE quote_post_id IS NOT NULL;

	ALTER TABLE audit_body_snapshot ADD COLUMN author_snapshot jsonb;
	ALTER TABLE audit_body_snapshot ADD COLUMN quote_author_snapshot jsonb;

	WITH payloads AS (
		SELECT COALESCE(last_success_payload, payload) AS value,
		       COALESCE(last_success_at, fetched_at, updated_at) AS observed_at
		FROM public_post_cache
		WHERE COALESCE(last_success_payload, payload) IS NOT NULL
	), candidates AS (
		SELECT value->'author' AS author, observed_at FROM payloads
		UNION ALL
		SELECT value->'quote'->'author' AS author, observed_at FROM payloads
	), latest AS (
		SELECT DISTINCT ON (author->>'id') author, observed_at
		FROM candidates
		WHERE author IS NOT NULL
		  AND COALESCE(author->>'id', '') ~ '^[0-9]{1,20}$'
		  AND COALESCE(author->>'name', '') <> ''
		  AND COALESCE(author->>'screenName', '') <> ''
		ORDER BY author->>'id', observed_at DESC
	)
	INSERT INTO x_user (user_id, name, screen_name, avatar_url, profile_observed_at, updated_at)
	SELECT author->>'id', author->>'name', author->>'screenName', NULLIF(author->>'avatarUrl', ''), observed_at, observed_at
	FROM latest;

	WITH payloads AS (
		SELECT COALESCE(last_success_payload, payload) AS value,
		       COALESCE(last_success_at, fetched_at, updated_at) AS observed_at
		FROM public_post_cache
		WHERE COALESCE(last_success_payload, payload) IS NOT NULL
	), quote_candidates AS (
		SELECT value->'quote' AS value, observed_at FROM payloads
	), quotes AS (
		SELECT DISTINCT ON (value->>'id') value, observed_at
		FROM quote_candidates
		ORDER BY value->>'id', observed_at DESC
	)
	INSERT INTO x_post (post_id, author_id, payload, observed_at, updated_at)
	SELECT value->>'id', value->'author'->>'id', value - 'author' - 'quote', observed_at, observed_at
	FROM quotes
	WHERE COALESCE(value->>'id', '') ~ '^[0-9]{2,20}$'
	  AND COALESCE(value->'author'->>'id', '') ~ '^[0-9]{1,20}$'
	ON CONFLICT (post_id) DO UPDATE SET
		author_id = EXCLUDED.author_id, payload = EXCLUDED.payload,
		observed_at = EXCLUDED.observed_at, updated_at = EXCLUDED.updated_at
	WHERE x_post.observed_at <= EXCLUDED.observed_at;

	WITH payloads AS (
		SELECT COALESCE(last_success_payload, payload) AS value,
		       COALESCE(last_success_at, fetched_at, updated_at) AS observed_at
		FROM public_post_cache
		WHERE COALESCE(last_success_payload, payload) IS NOT NULL
	)
	INSERT INTO x_post (post_id, author_id, payload, quote_post_id, observed_at, updated_at)
	SELECT value->>'id', value->'author'->>'id', value - 'author' - 'quote',
	       CASE WHEN quote.post_id IS NOT NULL THEN value->'quote'->>'id' END,
	       payloads.observed_at, payloads.observed_at
	FROM payloads
	LEFT JOIN x_post quote ON quote.post_id = value->'quote'->>'id'
	WHERE COALESCE(value->>'id', '') ~ '^[0-9]{2,20}$'
	  AND COALESCE(value->'author'->>'id', '') ~ '^[0-9]{1,20}$'
	ON CONFLICT (post_id) DO UPDATE SET
		author_id = EXCLUDED.author_id, payload = EXCLUDED.payload,
		quote_post_id = EXCLUDED.quote_post_id, observed_at = EXCLUDED.observed_at,
		updated_at = EXCLUDED.updated_at
	WHERE x_post.observed_at <= EXCLUDED.observed_at;
`}, {Version: 5, SQL: `
	CREATE TABLE rate_limit_settings (
		singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
		authenticated_per_hour integer NOT NULL CHECK (authenticated_per_hour BETWEEN 1 AND 10000),
		authenticated_per_day integer NOT NULL CHECK (authenticated_per_day BETWEEN 1 AND 100000),
		global_per_hour integer NOT NULL CHECK (global_per_hour BETWEEN 1 AND 100000),
		revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
		updated_at timestamptz NOT NULL DEFAULT now(),
		updated_by text
	);

	CREATE TABLE role_rate_limit (
		role_id text PRIMARY KEY CHECK (role_id ~ '^[A-Za-z0-9_-]{1,64}$'),
		per_hour integer NOT NULL CHECK (per_hour BETWEEN 1 AND 10000),
		per_day integer NOT NULL CHECK (per_day BETWEEN 1 AND 100000),
		updated_at timestamptz NOT NULL DEFAULT now(),
		updated_by text
	);
`}, {Version: 6, SQL: `
	UPDATE public_post_cache
	SET last_success_payload = NULL, last_success_at = NULL,
	    embed_token = NULL, embed_expires_at = NULL
	WHERE state <> 'ready';
`}}
