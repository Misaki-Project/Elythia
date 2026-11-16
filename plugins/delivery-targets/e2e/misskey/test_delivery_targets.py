"""Delivery targets against a real Misskey (#52).

Misskey の bob が Elythia の dtalice をフォローし、dtalice の投稿が bob に届くか、
Misskey から照会して見えるかを、投稿ごとの指定で確かめる。
"""

from __future__ import annotations

import pytest

from conftest import MISSKEY_DOMAIN  # type: ignore[import-not-found]
from conftest_base import MisskeyLikeClient  # type: ignore[import-not-found]
from dt_common import Case, MisskeyLikeQueue, cases, marker, note_url, post, settle, snapshot, unsigned_fetch_status  # type: ignore[import-not-found]

CASES = cases(MISSKEY_DOMAIN)


def _on_bob_timeline(misskey: MisskeyLikeClient, url: str) -> bool:
    return any(n.get("uri") == url for n in misskey._api("notes/timeline", {"limit": 50}))


@pytest.mark.parametrize("case", CASES, ids=[c.name for c in CASES])
def test_delivery(case: Case, mkgo: MisskeyLikeClient, misskey: MisskeyLikeClient, bob_follows_alice) -> None:
    m = marker()
    watches = [MisskeyLikeQueue(mkgo, "deliver", "Elythia deliver"), MisskeyLikeQueue(misskey, "inbox", "Misskey inbox")]
    before = snapshot(watches)
    note = post(mkgo, f"delivery-targets {case.name} {m}", case)
    url = note_url(mkgo, note["id"])
    sentinel = mkgo.create_note(f"sentinel {m}")["createdNote"]
    sentinel_url = note_url(mkgo, sentinel["id"])

    settle(watches, lambda: _on_bob_timeline(misskey, sentinel_url), before)
    assert _on_bob_timeline(misskey, url) is case.delivered, (
        f"{case.name}: expected delivered={case.delivered} to {MISSKEY_DOMAIN}"
    )


@pytest.mark.parametrize("case", CASES, ids=[c.name for c in CASES])
def test_fetch(case: Case, mkgo_unfollowed: MisskeyLikeClient, misskey: MisskeyLikeClient) -> None:
    note = post(mkgo_unfollowed, f"delivery-targets fetch {case.name} {marker()}", case)
    url = note_url(mkgo_unfollowed, note["id"])

    expected = 200 if case.unsigned_fetch else 404
    assert unsigned_fetch_status(url) == expected, f"{case.name}: anonymous GET of {url}"

    # Misskey の ap/show は、Misskey 自身の鍵で署名して取りに行く。
    try:
        resolved = misskey.resolve_ap(url)
        visible = resolved.get("type") == "Note"
    except RuntimeError:
        visible = False
    assert visible is case.signed_fetch, f"{case.name}: Misskey looks {url} up"
