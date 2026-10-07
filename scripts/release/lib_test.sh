#!/usr/bin/env bash
# Regression tests for lib.sh.
#
# These exist because of a specific incident in netscli, the project these
# scripts are ported from. `wait_for_asset` printed its retry progress to
# stdout, `verified_sha` calls it, and every caller does
# `sha=$(verified_sha ...)`, so the progress lines were captured as part of
# the checksum and written into the Homebrew and Scoop manifests. Every
# publish job failed, with an error naming the manifest rather than the
# cause.
#
# Nothing in shellcheck catches that, and the publish path only runs during
# a release, so a silent reintroduction would not surface until the next one.
#
# Run: bash scripts/release/lib_test.sh

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh disable=SC1091
. "${HERE}/lib.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

failures=0

ok() { echo "  ok   - $1"; }
bad() { echo "  FAIL - $1" >&2; failures=$((failures + 1)); }

sha256_of_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

# --- verified_sha returns the digest and nothing else ---------------------
#
# The stub forces the retry path. The first availability probe fails, so
# wait_for_asset emits one progress line before succeeding. That line is the
# contamination the original bug shipped.

ASSET_BYTES='nvx-fake-binary'
DIGEST="$(printf '%s' "$ASSET_BYTES" | sha256_of_stdin)"
PROBES="${TMP}/probe-count"
: >"$PROBES"

# These two shadow the real commands for the duration of the test. lib.sh
# calls them by name, which shellcheck cannot see, hence the SC2317s.
# shellcheck disable=SC2317
curl() {
  local args=("$@") i
  for ((i = 0; i < ${#args[@]}; i++)); do
    if [[ "${args[i]}" == "--head" ]]; then
      echo x >>"$PROBES"
      # Fail the first probe, succeed afterwards.
      [[ "$(wc -l <"$PROBES")" -ge 2 ]] && return 0
      return 1
    fi
    if [[ "${args[i]}" == "-o" ]]; then
      printf '%s' "$ASSET_BYTES" >"${args[i + 1]}"
      return 0
    fi
  done
  # No --head and no -o: this is the sidecar fetch.
  printf '%s  nvx-fake\n' "$DIGEST"
}

# Keep the retry from actually waiting 30 seconds.
# shellcheck disable=SC2317
sleep() { :; }

# gh records how it was called, prints to stdout as the real one does, and
# answers $GH_RESULT.
GH_CALLS="${TMP}/gh-calls"
GH_RESULT=0
# shellcheck disable=SC2317
gh() {
  echo "$* token=${GH_TOKEN:-}" >>"$GH_CALLS"
  echo "Loaded digest sha256:... for file://asset"
  return "$GH_RESULT"
}

GH_TOKEN=tap-pat ATTESTATION_GH_TOKEN=workflow-token
captured="$(verified_sha "https://example.invalid/download/v0.0.0" "nvx-fake")"

if [[ "$(wc -l <"$PROBES")" -lt 2 ]]; then
  bad "test setup: the retry path never engaged, so nothing was proven"
else
  ok "retry path engaged (the condition that triggered the original bug)"
fi

if [[ "$captured" =~ ^[0-9a-f]{64}$ ]]; then
  ok "verified_sha output is exactly one 64-char hex digest"
else
  bad "verified_sha output was contaminated: '${captured}'"
fi

if [[ "$captured" == "$DIGEST" ]]; then
  ok "verified_sha returned the digest of the downloaded bytes"
else
  bad "expected '${DIGEST}', got '${captured}'"
fi

# --- verified_sha checks build provenance --------------------------------
#
# The sidecar and the asset come from the same release page, so agreeing
# with each other proves nothing about where they were built. Only the npm
# script used to ask for release.yml's attestation.

if grep -q -- "attestation verify .* --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml token=workflow-token" "$GH_CALLS" 2>/dev/null; then
  ok "verified_sha asks gh for release.yml's attestation, with the attestation token"
else
  bad "verified_sha did not verify the attestation as expected: '$(cat "$GH_CALLS" 2>/dev/null)'"
fi

GH_RESULT=1
if unattested="$(verified_sha "https://example.invalid/download/v0.0.0" "nvx-fake" 2>/dev/null)"; then
  bad "verified_sha accepted an asset with no build provenance: '${unattested}'"
else
  ok "rejects an asset whose build provenance does not verify"
fi
GH_RESULT=0
unset GH_TOKEN ATTESTATION_GH_TOKEN

# --- verified_sha refuses a sidecar that disagrees with the bytes ---------
#
# The reason this function downloads the asset at all. A sidecar that is
# well formed and simply wrong must abort the publish rather than being
# copied into a manifest.

# shellcheck disable=SC2317
curl() {
  local args=("$@") i
  for ((i = 0; i < ${#args[@]}; i++)); do
    if [[ "${args[i]}" == "--head" ]]; then
      return 0
    fi
    if [[ "${args[i]}" == "-o" ]]; then
      printf '%s' "$ASSET_BYTES" >"${args[i + 1]}"
      return 0
    fi
  done
  # A valid-looking digest of something else entirely.
  printf '%s  nvx-fake\n' "$(printf 'not-the-asset' | sha256_of_stdin)"
}

if mismatch="$(verified_sha "https://example.invalid/download/v0.0.0" "nvx-fake" 2>/dev/null)"; then
  bad "verified_sha accepted a sidecar that disagreed with the bytes: '${mismatch}'"
else
  ok "rejects a sidecar whose digest is not the digest of the asset"
fi

unset -f curl sleep gh

# --- validate_tag ---------------------------------------------------------
#
# The tag reaches sed replacements and commit messages, so the rejection
# side matters more than the acceptance side.

for good in v0.6.0 v1.2.3 v10.20.30 v0.6.0-rc.1; do
  if validate_tag "$good" 2>/dev/null; then
    ok "accepts ${good}"
  else
    bad "rejected a valid tag: ${good}"
  fi
done

# The single quotes are the point. These are the literal strings an
# attacker would supply, and they must never be expanded here.
# shellcheck disable=SC2016
for evil in \
  'v0.6.0; rm -rf /' \
  'v0.6.0|p\nrm' \
  '$(id)' \
  'main' \
  '0.6.0' \
  'v0.6' \
  ''; do
  if validate_tag "$evil" 2>/dev/null; then
    bad "accepted a malformed tag: '${evil}'"
  else
    ok "rejects '${evil}'"
  fi
done

# --- ci_verdict -----------------------------------------------------------
#
# release.yml refuses to build from a commit whose CI did not pass. It used to
# look at every ci.yml run listed for the commit, so an old failed or cancelled
# run kept blocking after a newer run of the same commit had passed. The
# newest run decides now, and a newest run that failed or was cancelled still
# blocks. These are the shapes `gh run list --json` gives.

if ! command -v jq >/dev/null 2>&1; then
  bad "jq is not installed, so ci_verdict cannot be tested"
else
  # One run, as gh prints it: id, creation time, status, conclusion.
  run_json() {
    printf '{"databaseId":%s,"createdAt":"%s","status":"%s","conclusion":"%s"}' "$1" "$2" "$3" "$4"
  }
  check_verdict() { # description, wanted, runs as a JSON array
    local got
    # jq failing on unreadable input is the case under test, so it must not end the run.
    got="$(printf '%s' "$3" | ci_verdict 2>/dev/null)" || true
    if [[ "$got" == "$2" ]]; then
      ok "$1"
    else
      bad "$1: wanted '$2', got '$got'"
    fi
  }

  T1=2026-10-07T10:00:00Z
  T2=2026-10-07T11:00:00Z

  check_verdict "no run yet" none "[]"
  check_verdict "a queued run" running "[$(run_json 1 $T1 queued "")]"
  check_verdict "a run in progress" running "[$(run_json 1 $T1 in_progress "")]"
  check_verdict "a run that passed" passed "[$(run_json 1 $T1 completed success)]"
  check_verdict "a run that failed" failed "[$(run_json 1 $T1 completed failure)]"
  check_verdict "a run that was cancelled" failed "[$(run_json 1 $T1 completed cancelled)]"

  # The reported case. An older run failed and a newer one passed.
  check_verdict "an old failure, then a pass" passed \
    "[$(run_json 2 $T2 completed success),$(run_json 1 $T1 completed failure)]"
  check_verdict "an old cancellation, then a pass" passed \
    "[$(run_json 2 $T2 completed success),$(run_json 1 $T1 completed cancelled)]"
  check_verdict "the same, listed oldest first" passed \
    "[$(run_json 1 $T1 completed failure),$(run_json 2 $T2 completed success)]"

  # Strict. The newest run decides in the other direction too.
  check_verdict "an old pass, then a failure" failed \
    "[$(run_json 2 $T2 completed failure),$(run_json 1 $T1 completed success)]"
  check_verdict "an old pass, then a cancellation" failed \
    "[$(run_json 2 $T2 completed cancelled),$(run_json 1 $T1 completed success)]"
  check_verdict "an old pass, then a run that timed out" failed \
    "[$(run_json 2 $T2 completed timed_out),$(run_json 1 $T1 completed success)]"
  check_verdict "a newest run that was skipped" failed "[$(run_json 1 $T1 completed skipped)]"

  # Waiting wins over an old failure, so a newer run gets to finish.
  check_verdict "an old failure, then a run in progress" running \
    "[$(run_json 2 $T2 in_progress ""),$(run_json 1 $T1 completed failure)]"
  check_verdict "an old pass, then a queued run" running \
    "[$(run_json 2 $T2 queued ""),$(run_json 1 $T1 completed success)]"

  # The same creation time. The larger run id is the newer run.
  check_verdict "equal times, the larger id passed" passed \
    "[$(run_json 7 $T1 completed success),$(run_json 6 $T1 completed failure)]"
  check_verdict "equal times, the larger id failed" failed \
    "[$(run_json 6 $T1 completed success),$(run_json 7 $T1 completed failure)]"

  # Input jq cannot read gives no verdict, which the wait reads as no result yet.
  check_verdict "empty input" "" ""
  check_verdict "input that is not JSON" "" "not json"
fi

if [[ "$failures" -gt 0 ]]; then
  echo "${failures} failure(s)" >&2
  exit 1
fi
echo "all lib.sh tests passed"
