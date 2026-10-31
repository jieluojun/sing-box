#!/usr/bin/env bash
set -euo pipefail

if [[ "$EVENT" == "workflow_dispatch" ]]; then
  VERSION="${INPUT_VERSION#v}"
  if [[ -z "$VERSION" ]]; then
    # Use a tag reachable from the selected ref, if available. Include the
    # run ID and attempt so an automatic release never replaces another run.
    base=$(git describe --tags --abbrev=0 --match 'v[0-9]*' --exclude '*-auto.*' --exclude 'v0.0.0*' HEAD 2>/dev/null || true)
    base="${base#v}"
    base="${base%%+*}"
    if [[ ! "$base" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
      base=$(tr -d '\r\n' < release/BASE_VERSION)
      if [[ ! "$base" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ || "$base" == 0.0.0* ]]; then
        echo '::error::Invalid release/BASE_VERSION; set the source release version before building.' >&2
        exit 1
      fi
    fi
    VERSION="${base}-auto.${RUN_ID}.${RUN_ATTEMPT}"
  fi
  # Validate the release tag before deriving the display and artifact version.
  if [[ ! "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]]; then
    echo '::error::Invalid version: use letters, digits, dots, hyphens, underscores or +.' >&2
    exit 1
  fi
  TAG="v${VERSION}"
  PRE="$INPUT_PRE"
  SKIP_ANDROID="$INPUT_SKIP"
  SKIP_GUI="$INPUT_SKIP_GUI"
else
  TAG="$REF_NAME"
  VERSION="${TAG#v}"
  # The custom -xiaobaf14g.N suffix alone does not indicate a prerelease.
  if [[ "$VERSION" =~ -(alpha|beta|rc) ]]; then
    PRE=true
  else
    PRE=false
  fi
  SKIP_ANDROID=false
  SKIP_GUI=true
fi
# Release tags must remain unique, but binary versions and artifact names
# should contain only the upstream version, without fork/build identifiers.
VERSION="${VERSION%%-xiaobaf14g.*}"
VERSION="${VERSION%%-auto.*}"
if [[ -z "$VERSION" ]]; then
  echo '::error::Release version is empty after removing build suffix.' >&2
  exit 1
fi
{
  echo "version=$VERSION"
  echo "tag=$TAG"
  echo "prerelease=$PRE"
  echo "skip_android=$SKIP_ANDROID"
  echo "skip_gui=$SKIP_GUI"
} | tee -a "$GITHUB_OUTPUT"
