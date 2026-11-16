"""Fixtures for the delivery-targets e2e against a real Mastodon (#52)."""

from __future__ import annotations

import os
import sys

import httpx
import pytest

_HERE = os.path.dirname(os.path.abspath(__file__))
for _path in (os.path.dirname(_HERE), os.environ.get("FEDERATION_COMMON_DIR", "/tests/common")):
    if _path not in sys.path:
        sys.path.insert(0, _path)

from conftest_base import MisskeyLikeClient, poll_until, wait_for_health  # noqa: E402

MKGO_URL = os.environ.get("MKGO_URL", "https://mkgo")
MASTODON_URL = os.environ.get("MASTODON_URL", "https://mastodon")
MKGO_DOMAIN = os.environ.get("MKGO_DOMAIN", "mkgo")
MASTODON_DOMAIN = os.environ.get("MASTODON_DOMAIN", "mastodon")
MASTODON_TOKEN_FILE = os.environ.get("MASTODON_TOKEN_FILE", "/shared/mastodon_token")


class MastodonClient:
    """Minimal Mastodon REST client for the e2e."""

    def __init__(self, base_url: str, token: str):
        self.http = httpx.Client(
            base_url=base_url, timeout=30, verify=False,
            headers={"Authorization": f"Bearer {token}"},
        )

    def _check(self, resp: httpx.Response):
        if resp.status_code >= 400:
            raise RuntimeError(f"{resp.request.method} {resp.request.url} failed ({resp.status_code}): {resp.text[:500]}")
        return resp.json()

    def get(self, path: str, **params):
        return self._check(self.http.get(path, params=params))

    def post(self, path: str, **body):
        return self._check(self.http.post(path, json=body))

    def resolve_status(self, url: str) -> dict | None:
        """Look a remote post up through search (resolve=true), or None."""
        res = self.get("/api/v2/search", q=url, resolve="true", type="statuses")
        statuses = res.get("statuses") or []
        return statuses[0] if statuses else None

    def home_uris(self) -> set[str]:
        return {s.get("uri") for s in self.get("/api/v1/timelines/home", limit=40)}


@pytest.fixture(scope="session", autouse=True)
def wait_for_instances() -> None:
    wait_for_health(MKGO_URL, "/healthz")
    wait_for_health(MASTODON_URL, "/health")


@pytest.fixture(scope="session")
def mkgo() -> MisskeyLikeClient:
    """The Elythia admin (dtalice). Mastodon's alice follows this user."""
    client = MisskeyLikeClient(MKGO_URL, MKGO_DOMAIN)
    client.create_admin("dtalice", "password1234")
    client._api("admin/update-meta", {"federation": "all"})
    return client


@pytest.fixture(scope="session")
def mkgo_unfollowed(mkgo: MisskeyLikeClient) -> MisskeyLikeClient:
    """A second Elythia user (dtdave) nobody follows, for lookups."""
    client = MisskeyLikeClient(MKGO_URL, MKGO_DOMAIN)
    try:
        data = mkgo._api("admin/accounts/create", {"username": "dtdave", "password": "password1234"})
        client.token = data.get("token")
    except RuntimeError:
        client.signin("dtdave", "password1234")
    return client


@pytest.fixture(scope="session")
def mastodon() -> MastodonClient:
    with open(MASTODON_TOKEN_FILE) as f:
        token = f.read().strip()
    return MastodonClient(MASTODON_URL, token)


@pytest.fixture(scope="session")
def mastodon_follows_alice(mkgo: MisskeyLikeClient, mastodon: MastodonClient) -> None:
    """Make Mastodon's alice follow Elythia's dtalice and wait until it is accepted."""

    def lookup():
        found = mastodon.get("/api/v2/search", q=f"dtalice@{MKGO_DOMAIN}", resolve="true", type="accounts")
        accounts = found.get("accounts") or []
        return accounts[0] if accounts else None

    account = poll_until(lookup, timeout=90, interval=3, desc="Mastodon resolves dtalice@mkgo")
    mastodon.post(f"/api/v1/accounts/{account['id']}/follow")

    def following() -> bool:
        rels = mastodon.get("/api/v1/accounts/relationships", **{"id[]": account["id"]})
        return bool(rels and rels[0].get("following"))

    poll_until(following, timeout=90, interval=3, desc="Elythia accepts alice's follow")
