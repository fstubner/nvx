#!/usr/bin/env bash
# Publishes @fstubner/nvx and its five per-platform packages for a release.
#
# Runs from publish.yml on `release: published`, so npm gets a version only
# once the draft release.yml left has been reviewed and published by hand.
# It used to run inside release.yml, before the draft existed, which made the
# draft a review gate for GitHub and nothing else.
#
# The binaries come from the release's own assets rather than from a build
# here. Each one is checked against its .sha256 sidecar, and then against the
# build provenance release.yml attested for it, so a file swapped on the
# release page after the build is refused rather than published.
#
# optionalDependencies with os/cpu, NOT a postinstall download. nvx exists
# because `npm install` runs code from strangers before anyone has looked at
# it; shipping a postinstall to fetch our own binary would be the same trick.
# npm resolves the one matching package and executes nothing.
#
# Required environment:
#   NODE_AUTH_TOKEN - npm automation token. Absent means skip, so a release
#                     on a fork or before the secret exists is not a failure.
#   GH_TOKEN        - for `gh attestation verify`.
# Required argument:
#   $1 - tag, e.g. "v0.6.0".

set -euo pipefail

# shellcheck source=scripts/release/lib.sh
. "$(dirname "$0")/lib.sh"

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

TAG="${1:?usage: publish-npm.sh <tag>}"
validate_tag "$TAG"
VERSION="${TAG#v}"
BASE="https://github.com/fstubner/nvx/releases/download/${TAG}"

if [[ -z "${NODE_AUTH_TOKEN:-}" ]]; then
  echo "NPM_TOKEN is not configured; skipping the npm publish."
  echo "Add it as a repository secret to enable this step."
  exit 0
fi

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

# The token stays in the environment. This file names the variable and npm
# expands it when it reads the file, so no copy of the token is written to
# disk, and it is gone when this step's environment is.
# shellcheck disable=SC2016  # the literal ${NODE_AUTH_TOKEN} is the point
printf '//registry.npmjs.org/:_authToken=${NODE_AUTH_TOKEN}\n' >"$WORKDIR/npmrc"
export NPM_CONFIG_USERCONFIG="$WORKDIR/npmrc"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# fetch_binary <release asset> <destination>
fetch_binary() {
  local asset="$1" dest="$2" sha
  echo "-> verifying ${asset}"
  sha=$(verified_sha "$BASE" "$asset")
  mkdir -p "$(dirname "$dest")"
  curl -fsSL "${BASE}/${asset}" -o "$dest"
  if [[ "$(sha256_of "$dest")" != "$sha" ]]; then
    echo "ERROR: ${asset} changed between verifying it and downloading it" >&2
    return 1
  fi
  gh attestation verify "$dest" --repo fstubner/nvx \
    --signer-workflow fstubner/nvx/.github/workflows/release.yml >/dev/null
  echo "   checksum and build provenance match"
}

# Idempotent, like the other publish scripts: re-running publish.yml for a tag
# is its recovery path, and npm refuses to publish over an existing version.
publish_dir() {
  local dir="$1" name
  name=$(cd "$dir" && npm pkg get name | tr -d '"')
  if npm view "${name}@${VERSION}" version >/dev/null 2>&1; then
    echo "${name}@${VERSION} is already on npm; skipping"
    return 0
  fi
  (cd "$dir" && npm publish --access public --provenance)
}

cp -R "${REPO_ROOT}/npm" "$WORKDIR/npm"
NPM_DIR="$WORKDIR/npm"

fetch_binary nvx-linux-amd64  "$NPM_DIR/nvx-linux-x64/bin/nvx"
fetch_binary nvx-linux-arm64  "$NPM_DIR/nvx-linux-arm64/bin/nvx"
fetch_binary nvx-darwin-amd64 "$NPM_DIR/nvx-darwin-x64/bin/nvx"
fetch_binary nvx-darwin-arm64 "$NPM_DIR/nvx-darwin-arm64/bin/nvx"
fetch_binary nvx.exe          "$NPM_DIR/nvx-win32-x64/bin/nvx.exe"
chmod +x "$NPM_DIR"/nvx-{linux,darwin}-*/bin/nvx

# The platform packages go first: the wrapper depends on them, and a wrapper
# on the registry whose optional deps do not exist yet installs with no
# binary at all.
for pkg in nvx-linux-x64 nvx-linux-arm64 nvx-darwin-x64 nvx-darwin-arm64 nvx-win32-x64; do
  (cd "$NPM_DIR/$pkg" && npm version "$VERSION" --no-git-tag-version --allow-same-version >/dev/null)
  publish_dir "$NPM_DIR/$pkg"
done

(
  cd "$NPM_DIR/nvx"
  npm version "$VERSION" --no-git-tag-version --allow-same-version >/dev/null
  for pkg in linux-x64 linux-arm64 darwin-x64 darwin-arm64 win32-x64; do
    npm pkg set "optionalDependencies.@fstubner/nvx-${pkg}=${VERSION}"
  done
)
publish_dir "$NPM_DIR/nvx"
