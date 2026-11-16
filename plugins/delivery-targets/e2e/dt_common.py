"""Shared pieces of the delivery-targets federation e2e (#52).

tests/federation/ のスタック (Elythia と本物の Misskey / Mastodon) に重ねて動かす。
相手のサーバーは 1 台なので、投稿を分けて 5 通りの指定を試す。
"""

from __future__ import annotations

import uuid
import time
from dataclasses import dataclass
from typing import Protocol

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


class QueueWatch(Protocol):
    """One job queue whose drain the delivery verdict depends on."""

    label: str

    def pending(self) -> int:
        """Jobs waiting or running now."""

    def retrying(self) -> frozenset[str]:
        """IDs of jobs waiting for a retry or given up."""


class MisskeyLikeQueue:
    """A BullMQ-compatible queue seen through admin/queue/* (Elythia, Misskey)."""

    def __init__(self, admin: MisskeyLikeClient, queue: str, label: str):
        self.admin = admin
        self.queue = queue
        self.label = label

    def pending(self) -> int:
        stats = self.admin._api("admin/queue/stats", {})
        if self.queue not in stats:
            raise AssertionError(f"{self.label}: admin/queue/stats has no {self.queue!r}: {sorted(stats)}")
        counts = stats[self.queue]
        return sum(int(counts.get(k, 0) or 0) for k in ("waiting", "active", "prioritized"))

    def retrying(self) -> frozenset[str]:
        # admin/queue/jobs は state ごとに新しい順で返すので、件数の上限に
        # 当たっても新しく入ったジョブは必ず含まれる。
        jobs = self.admin._api("admin/queue/jobs", {"queue": self.queue, "state": ["delayed", "failed"]})
        return frozenset(str(j["id"]) for j in jobs)


def _wait_drained(w: QueueWatch, timeout: int = 120) -> None:
    """Wait until w has nothing waiting or running on two reads in a row.

    1 回だけ読むと、親のジョブが子のジョブを積んで終わる境目で、待ち行列を
    読んだ後に子が積まれ、実行中を読む前に親が終わると 0 に見える。
    """
    zeros = 0

    def drained() -> bool:
        nonlocal zeros
        zeros = zeros + 1 if w.pending() == 0 else 0
        return zeros >= 2

    poll_until(drained, timeout=timeout, interval=2, desc=f"{w.label} drains")


def snapshot(watches: list[QueueWatch]) -> dict[str, frozenset[str]]:
    """Record the retrying jobs of every queue before posting."""
    return {w.label: w.retrying() for w in watches}


def settle(watches: list[QueueWatch], sentinel_arrived, before: dict[str, frozenset[str]]) -> None:
    """Wait until a later, unrestricted note reached the peer and every queue drained.

    「届かない」を待ち時間だけで決めると、遅れて届いたときに見落とす。そこで、
    後から投稿した絞っていないノートが相手に着いたうえで、送る側 (Elythia の
    deliver) と受ける側 (相手の inbox) のキューが順に空になるのを待つ。相手は
    受信を並行に処理するので、後のノートが着いたことは、先のノートの処理が
    終わったことを意味しない (#53 のレビュー)。

    再試行待ち (delayed) は空になるのを待たない。起動直後に相手がまだ受け付け
    ずに失敗した配送などが、分単位で残るため。代わりに、投稿の前 (before) に
    無かったジョブが delayed / failed に入っていたら、後の再試行で届くかも
    しれず「届かない」の証明にならないので落とす。件数の和では、前からあった
    ものが同時に消えたときに見落とすので、ジョブの ID で比べる。どのノートの
    ジョブかは見ず、安全側に倒す。
    """
    poll_until(sentinel_arrived, timeout=120, interval=3, desc="the sentinel note reaches the peer")
    # 送る側から順に。送る側が空になってから受ける側を見れば、受ける側へ
    # 新しく入るものはもう無い。
    for w in watches:
        _wait_drained(w)
    # ジョブが終わってからタイムラインに出るまでの、相手のプロセスの中の間。
    time.sleep(3)
    for w in watches:
        new = w.retrying() - before[w.label]
        assert not new, (
            f"{w.label}: jobs {sorted(new)} moved to delayed or failed while checking; "
            "a restricted note may still arrive on retry"
        )
