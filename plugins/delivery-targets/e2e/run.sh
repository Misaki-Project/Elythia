#!/bin/bash
# delivery-targets の実連合 e2e (#52)。tests/federation の Misskey / Mastodon の
# スタックに重ね、プラグイン入りの Elythia で、投稿ごとの指定どおりに配送と照会が
# 止まるかを確かめる。
#
#   plugins/delivery-targets/e2e/run.sh misskey
#   plugins/delivery-targets/e2e/run.sh mastodon
#
# 前提: frontend/built がある (tests/federation/common/Dockerfile.mkgo が COPY する)。
set -euo pipefail

target="${1:-}"
case "$target" in
  misskey) peer=misskey-app ;;
  mastodon) peer=masto-web ;;
  *) echo "usage: $0 misskey|mastodon" >&2; exit 2 ;;
esac

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
cd "$REPO_ROOT"

COMPOSE=(-f "tests/federation/compose.${target}.yml" -f "plugins/delivery-targets/e2e/compose.${target}.yml")
PROJECT="mk-delivery-targets-${target}"
LOG_DIR="${DT_E2E_LOG_DIR:-/tmp/delivery-targets-e2e-logs}"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "===> saving diagnostics to $LOG_DIR"
    mkdir -p "$LOG_DIR"
    docker compose "${COMPOSE[@]}" --profile test ps -a > "$LOG_DIR/ps-$target.log" 2>&1 || true
    docker compose "${COMPOSE[@]}" --profile test logs --no-color > "$LOG_DIR/compose-$target.log" 2>&1 || true
  fi
  echo "===> cleanup"
  docker compose "${COMPOSE[@]}" --profile test down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Elythia の image はリポジトリをそのままビルドするので、プラグインを組み込む
# 生成物 (go.work と cmd/elythia/plugins_generated.go) を先に書き出す。
echo "===> stage 0: generate plugin wiring"
GOWORK=off go run ./tools/pluginbuild
grep -q 'delivery-targets' cmd/elythia/plugins_generated.go || {
  echo "FAIL: delivery-targets is not wired into the build (is it disabled?)" >&2
  exit 1
}

echo "===> stage 1: bring up Elythia + $target"
docker compose "${COMPOSE[@]}" up -d --build

echo "===> stage 1b: wait for app-mkgo + $peer healthy"
deadline=$(($(date +%s) + 300))
while :; do
  healthy=0
  for c in "${PROJECT}-app-mkgo-1" "${PROJECT}-${peer}-1"; do
    state=$(docker inspect --format '{{.State.Health.Status}}' "$c" 2>/dev/null || echo missing)
    [ "$state" = "healthy" ] && healthy=$((healthy + 1))
  done
  if [ "$healthy" = "2" ]; then
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "FAIL: not all containers became healthy within 300s"
    docker compose "${COMPOSE[@]}" ps
    exit 1
  fi
  sleep 3
done

echo "===> stage 2: delivery-targets scenarios (pytest)"
# --no-deps: run --build が app-mkgo を作り直すと、nginx が古い IP を持ち続けて
# 502 になる (tests/federation/run-mastodon-test.sh と同じ)。
docker compose "${COMPOSE[@]}" --profile test run --rm --build --no-deps test-runner

echo "===> all stages PASS"
