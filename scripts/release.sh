#!/usr/bin/env bash
# release.sh — tag and publish the first version of github.com/antlss/oapi
#
# Usage:
#   ./scripts/release.sh [VERSION]
#
# Example:
#   ./scripts/release.sh v0.1.0
#
# What it does (in order):
#   1. Tag the core module and push → triggers release.yml CI gate
#   2. Update each adapter go.mod: remove replace directive, pin to VERSION
#   3. Run go mod tidy in each adapter
#   4. Commit the go.mod/go.sum updates
#   5. Tag each adapter module and push all tags
#
# Prerequisites:
#   - git remote "origin" is set and you have push access
#   - Go is installed and in PATH
#   - Working tree is clean

set -euo pipefail

VERSION="${1:-v0.1.0}"

# Strip leading "v" for semver comparisons if needed
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "ERROR: VERSION must be in the form vX.Y.Z (got: $VERSION)"
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ADAPTERS=(gin fiber chi echo)

echo "==> Releasing core github.com/antlss/oapi @ $VERSION"
cd "$ROOT"

# Guard: working tree must be clean
if [[ -n "$(git status --porcelain)" ]]; then
  echo "ERROR: working tree is not clean. Commit or stash changes first."
  exit 1
fi

# ── Step 1: Tag and push the core module ────────────────────────────────────
echo ""
echo "── Step 1: Tag core $VERSION"
git tag "$VERSION"
git push origin "$VERSION"
echo "  ✓ pushed tag $VERSION — release.yml CI gate will now run"

# ── Step 2–4: Update adapter modules ────────────────────────────────────────
echo ""
echo "── Step 2: Update adapter go.mod files"

for adapter in "${ADAPTERS[@]}"; do
  dir="$ROOT/adapter/$adapter"
  mod="$dir/go.mod"

  echo "  adapter/$adapter"

  # Remove the replace directive
  sed -i '' '/^replace github\.com\/antlss\/oapi =>/d' "$mod"
  # Strip any blank line left behind by the removal
  sed -i '' -e '/^[[:space:]]*$/{ /./!d; }' "$mod" || true

  # Update the require line: v0.0.0 → VERSION
  sed -i '' "s|github.com/antlss/oapi v0.0.0|github.com/antlss/oapi $VERSION|g" "$mod"

  echo "    go mod tidy..."
  cd "$dir"
  go mod tidy
  cd "$ROOT"

  echo "    ✓ adapter/$adapter updated"
done

# ── Step 5: Commit the go.mod/go.sum changes ────────────────────────────────
echo ""
echo "── Step 3: Commit adapter go.mod/go.sum updates"
git add adapter/gin/go.mod  adapter/gin/go.sum  \
        adapter/fiber/go.mod adapter/fiber/go.sum \
        adapter/chi/go.mod  adapter/chi/go.sum  \
        adapter/echo/go.mod adapter/echo/go.sum
git commit -m "chore: release adapters $VERSION — pin core, drop replace directives"
git push origin main

# ── Step 6: Tag and push adapter modules ────────────────────────────────────
echo ""
echo "── Step 4: Tag adapter modules"
ADAPTER_TAGS=()
for adapter in "${ADAPTERS[@]}"; do
  tag="adapter/$adapter/$VERSION"
  git tag "$tag"
  ADAPTER_TAGS+=("$tag")
  echo "  tagged $tag"
done

git push origin "${ADAPTER_TAGS[@]}"
echo "  ✓ pushed all adapter tags"

echo ""
echo "Done! Released:"
echo "  github.com/antlss/oapi             $VERSION"
for adapter in "${ADAPTERS[@]}"; do
  echo "  github.com/antlss/oapi/adapter/$adapter  $VERSION"
done
echo ""
echo "pkg.go.dev pages will index within a few minutes."
echo "Trigger indexing manually if needed:"
echo "  GOPROXY=proxy.golang.org go install github.com/antlss/oapi@$VERSION"
