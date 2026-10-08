#!/usr/bin/env bash
# Linuxホスト専用。実パスワードを書いた配布コピーはroot所有・0600で保管し、Gitへ戻さない。
set +x
set -Eeuo pipefail
umask 077
IMAGE='ghcr.io/misaki-project/mk-genshin@sha256:de6a50f49e7298c5b418e47937007d492f6fbb5cf2a2ff2ab07deebcfcb63292'
CONTAINER='mk-go-production'
CONFIG='/home/misskey/cherrypick/.config/default.yml'
BACKUP_ROOT='/home/misaki/mk-update-backups'
# 実パスワードはサーバー上の非公開コピーのこの欄へ記入する。
# 空欄なら既存コンテナ/設定のパスワードを安全に読み取る（対話入力なし）。
DB_PASSWORD=''
DB_HOST='localhost'
DB_PORT='5432'
DB_NAME='mk1'
DB_USER='misskey'
DUMP_JOBS=2
HEALTH_TIMEOUT=180
HELPER="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/update-misaki-helper.py"
stage='preflight'
backup=''
old=''
mutation=0
old_renamed=0
new_created=0
healthy=0
tmp=''
migration_container=''
old_id=''
new_id=''

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
cleanup() {
  local rc=$?
  trap - EXIT
  if (( rc != 0 && mutation && ! healthy )); then
    [[ -z $migration_container ]] || docker stop "$migration_container" >/dev/null 2>&1 || true
    # 名前を変更する前に新旧を混同して起動しない。未知versionのDBへ旧imageを戻さない。
    if (( new_created )); then docker stop "$new_id" >/dev/null 2>&1 || true; fi
    if (( old_renamed )); then
      docker stop "$old_id" >/dev/null 2>&1 || true
    else
      docker stop "$old_id" >/dev/null 2>&1 || true
    fi
    printf '\n更新失敗: 段階=%s / backup=%s / 旧container=%s\n' "$stage" "$backup" "${old:-$CONTAINER}" >&2
    printf 'DBは自動復元しません。旧containerを起動せず、DB台帳とログを確認してください。\n' >&2
  fi
  [[ -z $tmp ]] || rm -f -- "$tmp/pgpass" "$tmp/inspect.json" "$tmp/image.json"
  [[ -z $tmp ]] || rmdir -- "$tmp" 2>/dev/null || true
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
case "${1:-}" in
  --help|-h)
    printf '%s\n' 'sudo bash update-misaki.sh --check' 'sudo bash update-misaki.sh' \
      '--check: 構成・DB接続・migration109または117/dirty=falseを確認（pull/更新なし）。' \
      '承認後: image取得→オンラインbackup/migration→短時間停止・最終DBbackup→新container起動・検査。' \
      'ホストOSは再起動しません。object storageのsnapshot/version保全は承認前に確認してください。'
    exit 0 ;;
  --check) check_only=1 ;;
  '') check_only=0 ;;
  *) die '不明な引数です' ;;
esac
(( $# <= 1 )) || die '引数が多すぎます'
(( EUID == 0 )) || die 'sudo bashで実行してください'
for tool in docker python3 psql pg_dump pg_restore tar flock mktemp sha256sum; do
  command -v "$tool" >/dev/null || die "$tool が必要です"
done
python3 -c 'import yaml' 2>/dev/null || die 'python3-yamlが必要です'
[[ -f $HELPER && -f $CONFIG ]] || die 'helperまたは設定ファイルがありません'
[[ $DUMP_JOBS =~ ^[1-9][0-9]*$ && $HEALTH_TIMEOUT =~ ^[1-9][0-9]*$ ]] || die '設定値が不正です'
mkdir -p "$BACKUP_ROOT"
chmod 700 "$BACKUP_ROOT"
exec 9>"$BACKUP_ROOT/.update.lock"
flock -n 9 || die '更新が実行中です'
tmp=$(mktemp -d "$BACKUP_ROOT/.preflight-XXXXXX")
docker inspect "$CONTAINER" > "$tmp/inspect.json"
old_id=$(docker inspect --format '{{.Id}}' "$CONTAINER")
old_image=$(docker inspect --format '{{.Image}}' "$CONTAINER")
docker image inspect "$old_image" > "$tmp/image.json"
python3 "$HELPER" validate "$tmp/inspect.json" "$CONFIG" "$tmp/image.json"
# パスワードをargvへ渡さずstdinで渡す。PGPASSWORDやxtraceも使わない。
printf '%s' "$DB_PASSWORD" | python3 "$HELPER" pgpass "$tmp/inspect.json" "$CONFIG" "$tmp/pgpass"
unset DB_PASSWORD
export PGPASSFILE="$tmp/pgpass" PGCONNECT_TIMEOUT=10
db_args=(--host="$DB_HOST" --port="$DB_PORT" --username="$DB_USER" --dbname="$DB_NAME" --no-password)
schema_state() { psql "${db_args[@]}" -X -A -t -v ON_ERROR_STOP=1 -c 'SELECT version::text || '\''|'\'' || dirty::text FROM public.schema_migrations;'; }
source_state=$(schema_state)
[[ $source_state == '109|false' || $source_state == '117|false' ]] || die 'DBはversion109または117/dirty=falseである必要があります'
python3 "$HELPER" validate "$tmp/inspect.json" "$CONFIG" "$tmp/image.json" "$source_state"
database_bytes=$(psql "${db_args[@]}" -X -A -t -v ON_ERROR_STOP=1 -c 'SELECT pg_database_size(current_database());')
python3 "$HELPER" capacity "$BACKUP_ROOT" "$database_bytes"
if (( check_only )); then
  printf '起動構成・DB接続・本体台帳%sを確認。pull/停止/migrationなし。\n' "$source_state"
  exit 0
fi
[[ -t 0 ]] || die '承認入力のため端末から実行してください'
printf '対象: %s\nimage: %s\n' "$CONTAINER" "$IMAGE"
printf '%s\n' '他のwriter・自動更新を停止/無効化し、object storageの復元可能な保全を確認してください。' \
  '本体は117へ更新/維持し、hsrは起動時にmigration4へ更新します。旧登録は再認証まで非公開になります。' \
  '旧imageの自動再起動・自動DB復元は行いません。元が1.5.0の場合はDB117へ旧版を起動できません。' \
  '承認後は追加入力なしで新containerの起動まで進みます。OS再起動はしません。'
read -r -p '上記確認済みで更新する場合は UPDATE mk-go-production と入力: ' answer
[[ $answer == 'UPDATE mk-go-production' ]] || die 'キャンセルしました（本番未変更）'
backup=$(mktemp -d "$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
printf '承認を受け付けました。以後は追加入力なしで実行します。ログ: %s/update.log\n' "$backup"
cp -- "$tmp/inspect.json" "$backup/container-before.json"
cp -- "$tmp/image.json" "$backup/image-before.json"
old="$CONTAINER-before-$(basename "$backup")"
migration_container="$CONTAINER-migrate-$(basename "$backup")"
printf '%s\n' "$old_image" > "$backup/image-before.txt"
printf '%s\n' "$source_state" > "$backup/schema-before.txt"
printf '%s\n' "$IMAGE" > "$backup/image-after.txt"
printf '%s\n' "$old" > "$backup/old-container.txt"
# 承認後のSIGHUPを無視し、出力を保護されたファイルへ固定する。SIGINT/TERM失敗時は安全停止。
trap '' HUP
exec >>"$backup/update.log" 2>&1
printf '開始: %s\n' "$(date -u +%FT%TZ)"
stage='pull'
docker pull "$IMAGE"
[[ $(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$IMAGE") == linux/amd64 ]] || die 'imageのplatformが不正です'
docker image inspect "$IMAGE" > "$backup/image-after.json"
python3 "$HELPER" environment "$backup/container-before.json" "$backup/image-after.json" "$backup/environment.list"
tar -czf "$backup/config.tar.gz" -C "$(dirname "$CONFIG")" -- "$(basename "$CONFIG")"
sha256sum "$CONFIG" > "$backup/config.sha256"
docker run --rm --network none --entrypoint /app/elythia "$IMAGE" help > "$backup/cli-help.txt"
stage='online-backup'
# 書込中でも整合したsnapshotを取得。これはmigration前の復元用（以後の書込は含まない）。
pg_dump "${db_args[@]}" --format=directory --jobs="$DUMP_JOBS" --file="$backup/database-before"
pg_restore --list "$backup/database-before" > "$backup/database-before-list.txt"
[[ $(schema_state) == "$source_state" ]] || die 'DB台帳が準備中に変化しました'
# 元構成と設定の競合変更を検出。自動更新との競争を継続したまま更新しない。
docker inspect "$CONTAINER" > "$tmp/inspect.json"
python3 "$HELPER" unchanged "$backup/container-before.json" "$tmp/inspect.json"
sha256sum --check "$backup/config.sha256"
stage='online-migration'
mutation=1
# 公式手順に従いCONCURRENTLY indexを旧サーバー稼働中に作成し、停止時間から外す。
# その間の自動再起動を禁止。失敗時は旧サーバーも止め、未知versionの起動を防ぐ。
docker update --restart=no "$old_id"
if [[ $source_state == '109|false' ]]; then
  docker run --rm --name "$migration_container" --network host --user 1001:1001 --workdir /app \
  --env-file "$backup/environment.list" \
  --mount "type=bind,source=$CONFIG,target=/app/.config/default.yml,readonly" \
    --entrypoint /app/elythia "$IMAGE" migrate -config /app/.config/default.yml -direction up > "$backup/migration.log" 2>&1
else
  printf '本体117維持。本体migrationは再実行せず、hsr migrationは新版起動時に適用します。\n' > "$backup/migration.log"
fi
[[ $(schema_state) == '117|false' ]] || die 'migration完了が117/dirty=falseではありません'
stage='stopping'
date -u +%FT%TZ > "$backup/downtime-start.txt"
docker stop "$old_id"
stage='final-backup'
# 切替直前の書込みを漏らさない最終snapshot。既にschema117なので単純に旧imageへは戻せない。
pg_dump "${db_args[@]}" --format=directory --jobs="$DUMP_JOBS" --file="$backup/database-final"
pg_restore --list "$backup/database-final" > "$backup/database-final-list.txt"
[[ $(schema_state) == '117|false' ]] || die '停止中のDB台帳が変化しました'
sha256sum --check "$backup/config.sha256"
stage='recreate'
docker rename "$old_id" "$old"
old_renamed=1
new_id=$(python3 "$HELPER" create "$backup/container-before.json" "$backup/environment.list" "$IMAGE" "$CONTAINER" "$CONFIG")
new_created=1
stage='starting'
docker start "$new_id"
deadline=$((SECONDS + HEALTH_TIMEOUT))
while :; do
  [[ $(docker inspect --format '{{.State.Running}}/{{.RestartCount}}' "$new_id") == true/0 ]] || die '新containerが停止/再起動しました'
  docker logs "$new_id" > "$backup/startup.log" 2>&1
  if docker exec "$new_id" /app/elythia healthcheck -config /app/.config/default.yml > "$backup/healthcheck.log" 2>&1; then
    if python3 "$HELPER" plugins "$backup/startup.log"; then break; fi
  fi
  (( SECONDS < deadline )) || die 'healthcheckまたは4プラグインの起動確認がタイムアウトしました'
  sleep 2
done
[[ $(schema_state) == '117|false' ]] || die '起動後のDB台帳が不正です'
psql "${db_args[@]}" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT string_agg(version::text, ',' ORDER BY version) FROM plugin_hsr.schema_migrations;" > "$backup/hsr-migrations.txt"
[[ $(cat "$backup/hsr-migrations.txt") == '1,2,3,4' ]] || die 'hsrの独立schema migration1〜4が揃っていません'
healthy=1
date -u +%FT%TZ > "$backup/downtime-end.txt"
stage='post-start'
docker inspect "$new_id" > "$backup/container-after.json"
# 旧containerの書込層保存を停止時間の外へ移す。失敗しても正常な新版を止めない。
docker export "$old_id" > "$backup/container-filesystem.tar"
[[ -s $backup/container-filesystem.tar ]] || die '旧containerの保存に失敗しました（新版は稼働継続）'
docker exec "$new_id" /app/elythia doctor -config /app/.config/default.yml > "$backup/doctor.log" 2>&1 || die 'doctor失敗（新版は稼働継続、診断ログを確認）'
printf '更新成功。4プラグイン・hsr0.2.0/migration4・healthcheck・doctor・本体version117を確認。\n'
printf 'backup=%s / 旧container=%s（停止・自動再起動無効）\n' "$backup" "$old"
printf '実ブラウザのログイン/投稿/画像/XP/原神/fedwatch/hsrは別途確認してください。\n'
