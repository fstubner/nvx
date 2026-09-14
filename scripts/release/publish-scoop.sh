#!/usr/bin/env bash
# Updates fstubner/scoop-bucket's bucket/nvx.json to point at the new
# release. Single asset (nvx.exe), so simpler than the Homebrew script.
# Version, url and hash, all swapped via jq.
#
# The bucket already holds netscli.json and netscli-gui.json. This script
# only ever writes bucket/nvx.json, so the three coexist.
#
# The manifest is patched rather than regenerated from
# packaging/scoop/nvx.json, unlike the Homebrew formula. jq rewrites named
# fields and leaves the rest of the document alone, so there is no block of
# the file that only the bucket's copy can carry. The render check below
# then proves the three fields actually changed.
#
# Required environment:
#   GH_TOKEN - PAT with `repo` scope on fstubner/scoop-bucket.
# Required argument:
#   $1 - tag, e.g. "v0.6.0".

set -euo pipefail

# shellcheck source=scripts/release/lib.sh
. "$(dirname "$0")/lib.sh"

# Absolute, because this script cds into the cloned bucket further down.
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TEMPLATE="${REPO_ROOT}/packaging/scoop/nvx.json"

TAG="${1:?usage: publish-scoop.sh <tag>}"
validate_tag "$TAG"
VERSION="${TAG#v}"

BASE="https://github.com/fstubner/nvx/releases/download/${TAG}"
ASSET="nvx.exe"

echo "-> verifying ${ASSET}"
sha=$(verified_sha "$BASE" "$ASSET")

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT
clone_tap "fstubner/scoop-bucket" "$WORKDIR/bucket"
cd "$WORKDIR/bucket"

manifest="bucket/nvx.json"

# The bucket holds netscli's manifests and does not yet hold this one, so
# the first release would otherwise die on jq reading a file that is not
# there. Seed it from the template. Every field jq touches below is
# overwritten, including the template's placeholder hash.
if [[ ! -f "$manifest" ]]; then
  echo "-> bucket/nvx.json does not exist yet; seeding it from the template"
  if [[ ! -f "$TEMPLATE" ]]; then
    echo "ERROR: scoop manifest template not found at ${TEMPLATE}" >&2
    exit 1
  fi
  mkdir -p bucket
  cp "$TEMPLATE" "$manifest"
fi

# No `#/nvx.exe` rename fragment. The release asset is already named
# nvx.exe, which is the name `bin` expects.
asset_url="${BASE}/${ASSET}"

jq --arg ver "$VERSION" \
   --arg url "$asset_url" \
   --arg sha "$sha" '
     .version = $ver
     | .architecture["64bit"].url  = $url
     | .architecture["64bit"].hash = $sha
   ' "$manifest" > "$manifest.tmp" && mv "$manifest.tmp" "$manifest"

# Sanity check: jq wrote what we asked, and the hash is a real digest.
if ! jq -e --arg ver "$VERSION" --arg sha "$sha" '
       .version == $ver
       and .architecture["64bit"].hash == $sha
       and (.architecture["64bit"].hash | test("^[0-9a-f]{64}$"))
     ' "$manifest" >/dev/null; then
  echo "ERROR: scoop manifest render check failed" >&2
  cat "$manifest" >&2
  exit 1
fi

git diff
git add "$manifest"
commit_and_push "nvx ${VERSION}"

echo "Scoop bucket updated to ${VERSION}"
