"""Shared pieces of the delivery-targets federation e2e (#52).

tests/federation/ のスタック (Elythia と本物の Misskey / Mastodon) に重ねて動かす。
相手のサーバーは 1 台なので、投稿を分けて 5 通りの指定を試す。
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass

import httpx

from conftest_base import MisskeyLikeClient, poll_until  # type: ignore[import-not-found]

# 指定に入れる「相手ではないサーバー」。実在しない名前にしてある。
OTHER_HOST = "other.example"
PLUGIN = "delivery-targets"


@dataclass(frozen=True)
class Case:
    name: str
    mode: str | None
    hosts: tuple[str, ...]
    delivered: bool
    signed_fetch: bool
    unsigned_fetch: bool

    def option(self) -> dict | None:
        if self.mode is None:
            return None
        return {"mode": self.mode, "hosts": list(self.hosts)}


def cases(peer: str) -> list[Case]:
    """The five choices of #52 against the peer server `peer`."""
    return [
        Case("none", None, (), delivered=True, signed_fetch=True, unsigned_fetch=True),
        Case("include-peer", "include", (peer,), delivered=True, signed_fetch=True, unsigned_fetch=False),
        Case("include-other", "include", (OTHER_HOST,), delivered=False, signed_fetch=False, unsigned_fetch=False),
        Case("exclude-peer", "exclude", (peer,), delivered=False, signed_fetch=False, unsigned_fetch=True),
        Case("exclude-other", "exclude", (OTHER_HOST,), delivered=True, signed_fetch=True, unsigned_fetch=True),
    ]


def marker() -> str:
    return uuid.uuid4().hex


def post(client: MisskeyLikeClient, text: str, case: Case) -> dict:
    """Post a public note with the case's choice, returning the created note."""
    extra = {}
    option = case.option()
    if option is not None:
        extra["pluginOptions"] = {PLUGIN: option}
    return client.create_note(text, **extra)["createdNote"]


def note_url(client: MisskeyLikeClient, note_id: str) -> str:
    return f"{client.base_url}/notes/{note_id}"


def unsigned_fetch_status(url: str) -> int:
    """GET a note as an anonymous ActivityPub client (no signature, no server URL in the User-Agent)."""
    resp = httpx.get(
        url,
        headers={"Accept": "application/activity+json", "User-Agent": "delivery-targets-e2e"},
        verify=False,
        timeout=20,
        follow_redirects=False,
    )
    return resp.status_code


def wait_deliver_queue_empty(admin: MisskeyLikeClient, timeout: int = 120) -> None:
    """Wait until Elythia has no delivery waiting or running.

    再試行待ち (delayed) は数えない。起動直後に相手がまだ受け付けずに失敗した
    配送などが、分単位で残るため。届くべきでないノートが送られていれば、
    先に投稿したものなので、後の目印のノートより前に相手へ着いている。
    """

    def empty() -> bool:
        stats = admin._api("admin/queue/stats", {})
        deliver = stats.get("deliver", {})
        return sum(int(deliver.get(k, 0) or 0) for k in ("waiting", "active")) == 0

    poll_until(empty, timeout=timeout, interval=2, desc="Elythia's deliver queue drains")


def settle(admin: MisskeyLikeClient, sentinel_arrived) -> None:
    """Wait until a later, unrestricted note reached the peer and nothing is left to deliver.

    「届かない」を待ち時間だけで決めると、遅れて届いたときに見落とす。後から
    投稿した絞っていないノートが相手に着き、さらに配送のキューが空になった時点で、
    先に投稿したノートが届くならもう届いている。
    """
    poll_until(sentinel_arrived, timeout=120, interval=3, desc="the sentinel note reaches the peer")
    wait_deliver_queue_empty(admin)
    # 相手の受信のキューで処理中のものを待つ。
    time.sleep(5)
