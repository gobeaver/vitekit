#!/usr/bin/env bash
#
# Rewrite vitekit's module path everywhere it appears.
#
# The path is a placeholder until the package has a permanent home. Getting it
# right by hand means editing four go.mod files (module lines, the adapters'
# require and replace directives), every import in every module, and the docs —
# so it is scripted, and the script verifies the result.
#
#   scripts/set-module-path.sh github.com/your-org/vitekit
#
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: $0 <new-module-path>" >&2
  echo "example: $0 github.com/gobeaver/beaver-kit/vitekit" >&2
  exit 1
fi

new_path="$1"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

old_path="$(awk '/^module /{print $2; exit}' go.mod)"
if [ -z "$old_path" ]; then
  echo "could not read the current module path from go.mod" >&2
  exit 1
fi
if [ "$old_path" = "$new_path" ]; then
  echo "module path is already $new_path; nothing to do"
  exit 0
fi

echo "rewriting $old_path -> $new_path"

# Only text files that are actually tracked, so build fixtures and any
# node_modules present in the working tree are left alone.
files="$(git ls-files -- '*.go' '*.mod' '*.md' '*.yml' '*.yaml' | grep -v '^LICENSE$' || true)"
changed=0
for file in $files; do
  if grep -q "$old_path" "$file" 2>/dev/null; then
    # A literal replacement is safe here: the path always appears in full.
    python3 - "$file" "$old_path" "$new_path" <<'PY'
import sys
path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, encoding='utf-8') as handle:
    body = handle.read()
with open(path, 'w', encoding='utf-8') as handle:
    handle.write(body.replace(old, new))
PY
    echo "  $file"
    changed=$((changed + 1))
  fi
done
echo "$changed file(s) updated"

# A new module path almost always sorts differently against the third-party
# imports beside it — renaming amedaz-labs to michael-amedaz, for instance, moves
# it past labstack — which leaves every import block that mixes the two out of
# order. gofmt re-sorts them; without this the verification below fails on the
# script's own output.
echo "reformatting"
gofmt -w .

echo "tidying modules"
for module in . adapter/gin adapter/fiber adapter/echo example; do
  (cd "$module" && GOWORK=off go mod tidy)
done

echo "verifying"
gofmt -l . | grep . && { echo "gofmt reported unformatted files" >&2; exit 1; } || true
for module in . adapter/gin adapter/fiber adapter/echo example; do
  (cd "$module" && go build ./... && go vet ./... && go test -count=1 ./... >/dev/null)
  echo "  ok $module"
done

cat <<EOF

Module path is now $new_path.

Next, per RELEASING.md: tag the core module first, then point each adapter at
that released version and drop its local replace directive before tagging it.
EOF
