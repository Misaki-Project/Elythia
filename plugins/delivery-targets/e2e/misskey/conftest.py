"""Fixtures for the delivery-targets e2e against a real Misskey (#52)."""

from __future__ import annotations

import os
import sys

import pytest

_HERE = os.path.dirname(os.path.abspath(__file__))
for _path in (os.path.dirname(_HERE), os.environ.get("FEDERATION_COMMON_DIR", "/tests/common")):
    if _path not in sys.path:
        sys.path.insert(0, _path)

from conftest_base import MisskeyLikeClient, poll_until, wait_for_health  # noqa: E402

MKGO_URL = os.environ.get("MKGO_URL", "https://mkgo")
MISSKEY_URL = os.environ.get("MISSKEY_URL", "https://misskey")
MKGO_DOMAIN = os.environ.get("MKGO_DOMAIN", "mkgo")
MISSKEY_DOMAIN = os.environ.get("MISSKEY_DOMAIN", "misskey")


@pytest.fixture(scope="session", autouse=True)
def wait_for_instances() -> None:
    wait_for_health(MKGO_URL, "/healthz")
    wait_for_health(MISSKEY_URL, "/api/ping", method="POST")


@pytest.fixture(scope="session")
def mkgo() -> MisskeyLikeClient:
    """The Elythia admin (dtalice). Misskey's bob follows this user."""
    client = MisskeyLikeClient(MKGO_URL, MKGO_DOMAIN)
    client.create_admin("dtalice", "password1234")
    # meta.federation の既定は none なので、連合を検証する側が有効化する。
    client._api("admin/update-meta", {"federation": "all"})
    return client


@pytest.fixture(scope="session")
def mkgo_unfollowed(mkgo: MisskeyLikeClient) -> MisskeyLikeClient:
    """A second Elythia user (dtdave) nobody follows, for lookups.

    照会を試すノートは、相手に配送されていないものでないと意味が無い (配送された
    ノートは相手の手元にあるので、照会しなくても見える)。
    """
    client = MisskeyLikeClient(MKGO_URL, MKGO_DOMAIN)
    try:
        data = mkgo._api("admin/accounts/create", {"username": "dtdave", "password": "password1234"})
        client.token = data.get("token")
    except RuntimeError:
        client.signin("dtdave", "password1234")
    return client


@pytest.fixture(scope="session")
def misskey() -> MisskeyLikeClient:
    client = MisskeyLikeClient(MISSKEY_URL, MISSKEY_DOMAIN)
    client.create_admin("bob", "password1234")
    client._api("admin/update-meta", {"federation": "all"})
    return client


@pytest.fixture(scope="session")
def bob_follows_alice(mkgo: MisskeyLikeClient, misskey: MisskeyLikeClient) -> None:
    """Make Misskey's bob follow Elythia's dtalice and wait until it is accepted."""
    alice = poll_until(
        lambda: misskey.users_show("dtalice", MKGO_DOMAIN),
        timeout=90, interval=3, desc="Misskey resolves dtalice@mkgo",
    )
    if not alice.get("isFollowing"):
        if not alice.get("hasPendingFollowRequestFromYou"):
            misskey.follow(alice["id"])
        poll_until(
            lambda: misskey.users_show("dtalice", MKGO_DOMAIN).get("isFollowing"),
            timeout=90, interval=3, desc="Elythia accepts bob's follow",
        )
