#!/usr/bin/env bash
set -euo pipefail

# Resolve only the reviewed public production plugin sources. Each fetched
# checkout is verified before its .git directory is removed and the source is
# moved into the build tree. No credentials are embedded in the URLs or files.
repo_root=${1:-.}
repo_root=$(cd "$repo_root" && pwd -P)
plugins_root="$repo_root/plugins"
[[ -d "$plugins_root" ]] || { echo "PLUGIN_ROOT_MISSING" >&2; exit 1; }

temp_parent=${RUNNER_TEMP:-${TMPDIR:-/tmp}}
temp_root=$(mktemp -d "$temp_parent/elythia-production-plugins.XXXXXX")
cleanup() { rm -rf -- "$temp_root"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

resolve_one() {
	local name=$1 url=$2 expected=$3 work="$temp_root/$1" dest="$plugins_root/$1"
	if [[ -e "$dest" || -L "$dest" ]]; then
		echo "PLUGIN_DESTINATION_EXISTS:$name" >&2
		return 1
	fi
	mkdir "$work"
	git -C "$work" init --quiet
	git -C "$work" remote add origin "$url"
	GIT_TERMINAL_PROMPT=0 git -C "$work" fetch --quiet --depth 1 origin "$expected"
	git -C "$work" checkout --quiet --detach FETCH_HEAD
	local actual_sha actual_url dirty
	actual_sha=$(git -C "$work" rev-parse HEAD)
	actual_url=$(git -C "$work" remote get-url origin)
	dirty=$(git -C "$work" status --porcelain --untracked-files=all)
	if [[ "$actual_sha" != "$expected" || "$actual_url" != "$url" || -n "$dirty" ]]; then
		echo "PLUGIN_CHECKOUT_PIN_MISMATCH:$name" >&2
		return 1
	fi
	rm -rf -- "$work/.git"
	mv -- "$work" "$dest"
	printf 'VERIFIED_PRODUCTION_PLUGIN name=%s revision=%s\n' "$name" "$actual_sha"
}

resolve_one genshin \
	https://github.com/Misaki-Project/mk-plugin-genshin.git \
	9a50079ec9d198ab44e4b9ff24d0a408109c09da
resolve_one fedwatch \
	https://github.com/Elythia-Network/elythia-plugin-fedwatch.git \
	af105a020656aaba121998a0bd5a51bee232dec8
resolve_one hsr \
	https://github.com/Misaki-Project/elythia-plugin-hsr.git \
	820068d720654650d5dc05d4136ad9f32f677fef
