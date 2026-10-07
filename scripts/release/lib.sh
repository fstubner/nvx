#!/usr/bin/env bash
# Shared helpers for the publish-*.sh scripts and release.yml.
#
# Source this, don't execute it:
#   . "$(dirname "$0")/lib.sh"
#
# Ported from netscli's scripts/release/lib.sh, where one implementation
# shared by every publish script is what stopped the same handful of bugs
# being copy-pasted per registry. The same reasoning applies here even at
# two scripts, because the third caller is publish.yml itself.

# --- Tag validation ------------------------------------------------------
#
# Every publish script interpolates the tag into shell strings, sed
# replacements, and commit messages. `workflow_dispatch` lets a caller
# supply an arbitrary tag input, so validate before it reaches any of
# those. Accepts `v1.2.3` and `v1.2.3-rc.1`, rejects everything else,
# including anything containing shell metacharacters.
validate_tag() {
  local tag="$1"
  if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]]; then
    echo "ERROR: refusing to publish malformed tag: '${tag}'" >&2
    echo "       expected vMAJOR.MINOR.PATCH[-prerelease]" >&2
    return 1
  fi
}

# --- CI verdict ----------------------------------------------------------
#
# release.yml waits for CI on the commit it is about to release. Reads the
# JSON that this prints:
#   gh run list --workflow=ci.yml --commit "$sha" --json status,conclusion,createdAt,databaseId
# and says what the wait should do:
#
#   none     no run yet
#   running  the newest run has not finished
#   passed   the newest run finished with success
#   failed   the newest run finished any other way, cancelled included
#
# Input that jq cannot read prints nothing, and the wait treats that as no result
# yet. Only the newest run counts. This used to look at every run listed for the
# commit, so one old failed or cancelled run blocked a release after a newer
# run of the same commit had passed. The newest run is the one with the latest
# creation time, and the larger run id settles a tie. A re-run of a run is the
# same run, and `gh run list` shows it with its newest attempt.
#
# Usage: verdict=$(printf '%s' "$runs" | ci_verdict)
ci_verdict() {
  jq -r '
    if length == 0 then "none"
    else
      (sort_by([.createdAt, .databaseId]) | .[-1]) as $newest
      | if $newest.status != "completed" then "running"
        elif $newest.conclusion == "success" then "passed"
        else "failed"
        end
    end'
}

# --- Asset availability --------------------------------------------------
#
# release.yml builds on the tag push and publishes a DRAFT, so by the time
# `release: published` fires the assets are normally already attached. The
# poll is for the other two ways in. A `workflow_dispatch` re-run can be
# aimed at a tag whose release.yml run is still building, and release.yml
# waits on CI before it builds anything, which puts twenty minutes between
# the tag and the first uploaded byte. Poll for up to 15 minutes rather
# than failing instantly on an asset that is merely late.
#
# Progress goes to stderr, deliberately. `verified_sha` calls this, and every
# caller of `verified_sha` uses a command substitution, so anything written
# to stdout here is captured as part of the checksum. That is not
# hypothetical. In netscli, with the retry engaged, `sha` became
#
#   "  not yet; retry in 30s (1/30)\n<the real digest>"
#
# which the downstream patch wrote into the formula, and the render check
# then rejected with "expected 4 valid sha256 lines, got 0". The error named
# the manifest rather than the cause. `lib_test.sh` pins it.
wait_for_asset() {
  local url="$1"
  local i
  for i in {1..30}; do
    if curl -fsSL --head "$url" >/dev/null 2>&1; then
      return 0
    fi
    echo "  not yet; retry in 30s ($i/30)" >&2
    sleep 30
  done
  echo "ERROR: $url never became available" >&2
  return 1
}

# --- Checksum verification -----------------------------------------------
#
# Print the SHA256 for an asset, having actually verified it.
#
# Reading the `.sha256` sidecar and trusting it is circular. The sidecar and
# the asset come from the same origin, so a bad sidecar produces a bad
# manifest and nothing notices. It also accepts an empty value, because the
# downstream `grep -c 'sha256 "'` guards match `sha256 ""` happily, so a
# truncated sidecar could publish a formula with blank hashes.
#
# Instead: fetch the sidecar, fetch the asset, hash the asset locally, and
# require all three of (sidecar is 64 hex chars), (local hash is 64 hex
# chars), (they match). Any failure aborts the publish.
#
# That still only proves the two files agree, and both come from the release
# page. So the asset must also carry the build provenance release.yml
# attested for it. A file swapped on the release page after the build, with
# a sidecar to match, fails here. This check used to live in the npm script
# only, so Homebrew and Scoop published whatever the page held.
#
# `gh` reads ATTESTATION_GH_TOKEN when it is set, and GH_TOKEN otherwise. The
# Homebrew and Scoop jobs hold a PAT for their own repository in GH_TOKEN,
# and hand this check the workflow's token instead.
#
# Usage: sha=$(verified_sha "$BASE" "nvx-linux-amd64")
verified_sha() {
  local base="$1" asset="$2"
  local sidecar_url="${base}/${asset}.sha256"
  local asset_url="${base}/${asset}"
  local workdir expected actual

  wait_for_asset "$sidecar_url" || return 1

  expected=$(curl -fsSL "$sidecar_url" | awk 'NR==1 {print $1}')
  if [[ ! "$expected" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "ERROR: ${asset}.sha256 is not a 64-char hex digest: '${expected}'" >&2
    return 1
  fi
  expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')

  workdir=$(mktemp -d)
  # shellcheck disable=SC2064  # intentional: expand workdir now, not at trap time
  trap "rm -rf '$workdir'" RETURN

  if ! curl -fsSL "$asset_url" -o "$workdir/asset"; then
    echo "ERROR: could not download ${asset_url} to verify its checksum" >&2
    return 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$workdir/asset" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "$workdir/asset" | awk '{print $1}')
  fi
  actual=$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')

  if [[ ! "$actual" =~ ^[0-9a-f]{64}$ ]]; then
    echo "ERROR: could not compute a SHA256 for ${asset}" >&2
    return 1
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "ERROR: checksum mismatch for ${asset}" >&2
    echo "       sidecar says: ${expected}" >&2
    echo "       actual bytes: ${actual}" >&2
    return 1
  fi

  # stdout to stderr: every caller captures this function's stdout as the
  # digest (see wait_for_asset).
  if ! GH_TOKEN="${ATTESTATION_GH_TOKEN:-${GH_TOKEN:-}}" gh attestation verify "$workdir/asset" \
      --repo fstubner/nvx \
      --signer-workflow fstubner/nvx/.github/workflows/release.yml >&2; then
    echo "ERROR: ${asset} has no build provenance from release.yml" >&2
    return 1
  fi

  printf '%s' "$actual"
}

# --- Repo checkout -------------------------------------------------------
#
# Clone without putting GH_TOKEN in the remote URL. A token in the URL is
# written to .git/config and can surface in git's error output on a
# failed push. `http.extraheader` keeps it in the config as a header
# value that git redacts.
#
# Usage: clone_tap "fstubner/homebrew-tap" "$WORKDIR/tap"
clone_tap() {
  local repo="$1" dest="$2"
  : "${GH_TOKEN:?GH_TOKEN must be set}"
  local auth
  auth=$(printf 'x-access-token:%s' "$GH_TOKEN" | base64 | tr -d '\n')

  git -c "http.https://github.com/.extraheader=Authorization: Basic ${auth}" \
    clone --depth 1 "https://github.com/${repo}.git" "$dest"

  git -C "$dest" config "http.https://github.com/.extraheader" \
    "Authorization: Basic ${auth}"
  git -C "$dest" config user.name  "nvx release bot"
  git -C "$dest" config user.email "noreply@nvx.run"
}

# --- Commit and push -----------------------------------------------------
#
# Idempotent. Re-running a publish for the same tag is the recovery path
# publish.yml's header offers, and `git commit` exits non-zero with nothing
# staged, which `set -e` would turn into a failure. Retries the push so a concurrent
# sibling job racing on the same repo doesn't lose the update. The tap and
# the bucket each hold manifests for more than one project, so that race is
# real rather than theoretical.
commit_and_push() {
  local message="$1"
  local i

  if git diff --cached --quiet; then
    echo "No changes to commit. Already up to date for ${message}."
    return 0
  fi

  git commit -m "$message"

  for i in 1 2 3; do
    if git push origin HEAD; then
      return 0
    fi
    echo "  push failed (attempt ${i}/3); rebasing on origin and retrying"
    git pull --rebase origin HEAD || true
    sleep 5
  done

  echo "ERROR: could not push ${message} after 3 attempts" >&2
  return 1
}
