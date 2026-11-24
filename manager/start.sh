#!/bin/sh
set -eu
umask 077

source_dir=${MANAGER_SOURCE_DIR:-/opt/manager/easel}
state_dir=${MANAGER_DATA_DIR:-/var/lib/manager}
app_dir=${MANAGER_APP_DIR:-/opt/manager/runtime}
project="$state_dir/project"
upstream_revision=011e10d86711c7ace3cbb01153f8915dbd018020
revision="$upstream_revision:$(cksum < "$source_dir/web/frontend/dist/index.html")"

export HOME="$state_dir/home"
mkdir -p "$project" "$HOME"
if [ "$(cat "$project/.nous-source-revision" 2>/dev/null || true)" != "$revision" ]; then
  cp -R "$source_dir/." "$project/"
  printf '%s\n' "$revision" > "$project/.nous-source-revision"
fi

export MANAGER_WORKSPACE="$project"
export PYTHONPATH="$app_dir:$project${PYTHONPATH:+:$PYTHONPATH}"
cd "$project"
exec "${MANAGER_BINARY:-/usr/local/bin/manager}"
