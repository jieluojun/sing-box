#!/usr/bin/env bash
set -euo pipefail

existing=$(git rev-parse -q --verify "refs/tags/${TAG}^{commit}" || true)
if [ -n "$existing" ] && [ "$existing" != "$(git rev-parse HEAD)" ]; then
  echo "::error::Tag $TAG already points to another commit; refusing to overwrite it." >&2
  exit 1
fi
# List all pages, including drafts. A failed API call is an error, not proof
# that a tag is free. Do not delete or update any existing release.
releases=$(gh api --paginate "repos/${REPO}/releases?per_page=100" --jq '.[].tag_name')
if grep -Fxq -- "$TAG" <<< "$releases"; then
  echo "::error::Release $TAG already exists; refusing to replace it." >&2
  exit 1
fi
