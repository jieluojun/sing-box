#!/usr/bin/env bash
set -euo pipefail

if [[ "$EVENT" == "workflow_dispatch" ]]; then
  VERSION="${INPUT_VERSION#v}"
  if [[ -z "$VERSION" ]]; then
    # Automatic sing-box-* releases are not version baselines. Use a reachable
    # upstream/fork v-tag or the checked-in source baseline instead.
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
    VERSION="$base"
  fi
  if [[ ! "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]]; then
    echo '::error::Invalid version: use letters, digits, dots, hyphens, underscores or +.' >&2
    exit 1
  fi
  PRE="$INPUT_PRE"
  SKIP_ANDROID="$INPUT_SKIP"
  SKIP_GUI="$INPUT_SKIP_GUI"
else
  VERSION="${REF_NAME#v}"
  if [[ "$VERSION" =~ -(alpha|beta|rc) ]]; then
    PRE=true
  else
    PRE=false
  fi
  SKIP_ANDROID=false
  SKIP_GUI=true
fi

# Keep the full upstream version for the embedded kernel and client package
# versions. The display/artifact version omits only a zero patch component.
VERSION="${VERSION%%-xiaobaf14g.*}"
VERSION="${VERSION%%-auto.*}"
if [[ -z "$VERSION" ]]; then
  echo '::error::Release version is empty after removing build suffix.' >&2
  exit 1
fi
DISPLAY_VERSION="$VERSION"
if [[ "$DISPLAY_VERSION" =~ ^([0-9]+\.[0-9]+)\.0(-|$)(.*)$ ]]; then
  DISPLAY_VERSION="${BASH_REMATCH[1]}${BASH_REMATCH[2]}${BASH_REMATCH[3]}"
fi
COMMIT=$(git rev-parse --verify HEAD^{commit})
SHORT_COMMIT="${COMMIT:0:9}"
ARTIFACT_VERSION="${DISPLAY_VERSION}-${SHORT_COMMIT}"
TAG="sing-box-${ARTIFACT_VERSION}"
if ! git check-ref-format "refs/tags/$TAG"; then
  echo '::error::Invalid derived release tag.' >&2
  exit 1
fi
{
  echo "version=$VERSION"
  echo "artifact_version=$ARTIFACT_VERSION"
  echo "tag=$TAG"
  echo "prerelease=$PRE"
  echo "skip_android=$SKIP_ANDROID"
  echo "skip_gui=$SKIP_GUI"
} | tee -a "$GITHUB_OUTPUT"
