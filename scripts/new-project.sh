#!/usr/bin/env bash
set -euo pipefail

name=${1:-}
description=${2:-A small product experiment.}

if [[ ! "$name" =~ ^[a-z][a-z0-9-]*$ ]]; then
  echo "NAME must be a lowercase slug (letters, numbers, hyphens)." >&2
  exit 1
fi

source_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
target_dir=$(dirname "$source_dir")/$name

if [[ -e "$target_dir" ]]; then
  echo "Target already exists: $target_dir" >&2
  exit 1
fi

mkdir -p "$target_dir"
rsync -a \
  --exclude .git \
  --exclude .claude/skills/incubate \
  --exclude .env \
  --exclude bin \
  --exclude node_modules \
  --exclude playwright-report \
  --exclude test-results \
  "$source_dir/" "$target_dir/"

find "$target_dir" -type f -not -path '*/.git/*' -print0 | xargs -0 perl -pi -e \
  "s/github.com\/maclir\/peyk/github.com\/maclir\/$name/g; s/peyk-test/${name}-test/g; s/peyk/$name/g; s/peyk/$name/g"

perl -0pi -e "s/# $name\n/# $name\n\n$description\n/" "$target_dir/README.md"
git -C "$target_dir" init -b main
git -C "$target_dir" add .

echo "Created $target_dir"
echo "Next: cd $target_dir && make test"
