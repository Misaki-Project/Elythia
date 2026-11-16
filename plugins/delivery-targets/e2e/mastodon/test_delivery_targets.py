"""Delivery targets against a real Mastodon (#52).

Mastodon の alice が Elythia の dtalice をフォローし、dtalice の投稿が alice の
ホームに届くか、Mastodon から検索 (照会) して見えるかを、投稿ごとの指定で確かめる。
"""

from __future__ import annotations

import pytest

from conftest import MASTODON_DOMAIN, MastodonClient  # type: ignore[import-not-found]
from conftest_base import MisskeyLikeClient  # type: ignore[import-not-found]
from dt_common import Case, cases, deliver_retrying, marker, note_url, post, settle, unsigned_fetch_status  # type: ignore[import-not-found]

CASES = cases(MASTODON_DOMAIN)


@pytest.mark.parametrize("case", CASES, ids=[c.name for c in CASES])
def test_delivery(case: Case, mkgo: MisskeyLikeClient, mastodon: MastodonClient, mastodon_follows_alice) -> None:
    m = marker()
    retrying_before = deliver_retrying(mkgo)
    note = post(mkgo, f"delivery-targets {case.name} {m}", case)
    url = note_url(mkgo, note["id"])
    sentinel = mkgo.create_note(f"sentinel {m}")["createdNote"]
    sentinel_url = note_url(mkgo, sentinel["id"])

    settle(mkgo, lambda: sentinel_url in mastodon.home_uris(), retrying_before)
    assert (url in mastodon.home_uris()) is case.delivered, (
        f"{case.name}: expected delivered={case.delivered} to {MASTODON_DOMAIN}"
    )


@pytest.mark.parametrize("case", CASES, ids=[c.name for c in CASES])
def test_fetch(case: Case, mkgo_unfollowed: MisskeyLikeClient, mastodon: MastodonClient) -> None:
    note = post(mkgo_unfollowed, f"delivery-targets fetch {case.name} {marker()}", case)
    url = note_url(mkgo_unfollowed, note["id"])

    expected = 200 if case.unsigned_fetch else 404
    assert unsigned_fetch_status(url) == expected, f"{case.name}: anonymous GET of {url}"

    # Mastodon の検索 (resolve=true) は、Mastodon から投稿を取りに行く。
    visible = mastodon.resolve_status(url) is not None
    assert visible is case.signed_fetch, f"{case.name}: Mastodon looks {url} up"
