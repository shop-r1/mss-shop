#!/bin/sh

set -eu

memory_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$memory_root"

for memory_file in \
  AGENTS.md \
  .codex/config.toml \
  tools/run-mss-mcp.sh \
  docs/project/status.md \
  docs/project/legacy-rebuild-status.yaml \
  docs/project/legacy-restoration-gap.md \
  docs/project/mall-settings-development.md \
  docs/project/member-levels-development.md \
  docs/governance/memory.md \
  docs/migration/legacy-tables.yaml \
  docs/migration/legacy-admin-acceptance-matrix.md \
  docs/acceptance/mall-local-browser-acceptance.md \
  docs/runbooks/ci-images.md \
  .agents/skills/r1shop-legacy-module/SKILL.md
do
  if [ ! -s "$memory_file" ]; then
    echo "project memory is missing: $memory_file" >&2
    exit 1
  fi
done

if ! command -v mss >/dev/null 2>&1; then
  echo "project memory requires the exact MSS 1.3.7 tool on PATH" >&2
  exit 1
fi

mss_identity=$(mss --version)
expected_mss_identity="mss version v1.3.7 (commit 77b53d41092741eac62fa6418c0bdbf87413c7cd"
case "$mss_identity" in
  "$expected_mss_identity"*) ;;
  *)
    echo "project memory requires MSS 1.3.7 commit 77b53d41092741eac62fa6418c0bdbf87413c7cd" >&2
    exit 1
    ;;
esac

GOWORK=off go test ./contracts
mss skills validate
git diff --check
git diff --cached --check

git ls-files --others --exclude-standard | while IFS= read -r untracked_file
do
  if [ -f "$untracked_file" ] && LC_ALL=C grep -Iq . "$untracked_file"; then
    if LC_ALL=C grep -nE '[[:blank:]]+$' "$untracked_file" >/dev/null; then
      echo "untracked project file has trailing whitespace: $untracked_file" >&2
      exit 1
    fi
  fi
done

echo "Project memory contracts passed."
