#!/usr/bin/env bash
# Authenticode-sign every .exe in a directory.
#
#   CERTUM_EMAIL=... CERTUM_OTP=... scripts/release/sign-windows.sh <dir>
#
# Signs in place: each .exe directly under <dir> comes back signed and
# timestamped, and the script fails if any of them does not verify afterwards.
#
# Ported from netscli's scripts/release/sign-windows.sh, which signs with the
# same Certum certificate, less its MSI half: nvx ships one executable.
#
# Why on Linux: the signing key is a Certum cloud certificate, not a file.
# Since June 2023 a publicly trusted code-signing key has to live on a token,
# an HSM or a cloud service, so there is no .pfx to hand `signtool`. Reaching
# Certum's cloud is plain HTTPS, so where this runs stops mattering.
#
# `ssign` reads the credentials from the environment itself, so they never
# reach a command line, the process table or a CI log.

set -euo pipefail

SSIGN_VERSION="v0.1.6"
SSIGN_SHA256="9bcd249c7250a9ee38bd68c28aadf662dd714854b3355647b3f55b30162b5b6b"
BASE="https://github.com/Le-Syl21/ssign/releases/download/${SSIGN_VERSION}"

ART_DIR="${1:-}"
if [[ -z "$ART_DIR" || ! -d "$ART_DIR" ]]; then
  echo "usage: $0 <directory of .exe files to sign>" >&2
  exit 2
fi
ART_DIR="$(cd "$ART_DIR" && pwd)"

: "${CERTUM_EMAIL:?CERTUM_EMAIL must be set}"
: "${CERTUM_OTP:?CERTUM_OTP must be set (base32 TOTP seed or otpauth:// URI)}"

for tool in curl tar osslsigncode; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ERROR: $tool is not installed" >&2; exit 1; }
done

shopt -s nullglob
exes=("${ART_DIR}"/*.exe)
shopt -u nullglob

# Nothing to sign is a failure, not a no-op: the executable this job exists for
# never arrived.
if [[ ${#exes[@]} -eq 0 ]]; then
  echo "ERROR: no .exe in ${ART_DIR}. Nothing to sign, which is not a success." >&2
  exit 1
fi

TOOLS="$(mktemp -d)"
trap 'rm -rf "$TOOLS"' EXIT

# Pinned by version AND by digest. The tarball is fetched over the network and
# then handed the signing credentials, so "the tag moved" and "the asset was
# replaced" are both worth refusing rather than discovering afterwards.
echo "Fetching ssign ${SSIGN_VERSION}"
curl -fsSL -o "${TOOLS}/ssign.tar.gz" "${BASE}/ssign-linux-x86_64.tar.gz"
got="$(sha256sum "${TOOLS}/ssign.tar.gz" | awk '{print $1}')"
if [[ "$got" != "$SSIGN_SHA256" ]]; then
  echo "ERROR: ssign does not match its pinned digest" >&2
  echo "       expected ${SSIGN_SHA256}" >&2
  echo "       got      ${got}" >&2
  exit 1
fi
tar xzf "${TOOLS}/ssign.tar.gz" -C "$TOOLS"
chmod +x "${TOOLS}/ssign"

# ssign timestamps with Certum's RFC3161 server (time.certum.pl, per its
# README). Without a timestamp a signature stops verifying the day the
# certificate expires.
for exe in "${exes[@]}"; do
  echo "Signing $(basename "$exe")"
  "${TOOLS}/ssign" "$exe"
done

# Read every signature back rather than trusting exit codes: a silently
# unsigned executable looks exactly like a signed one until someone downloads
# it.
#
# Both CA files are needed. The chain the signature carries ends at a Certum
# root that is in the distribution's CA store but not in osslsigncode's default,
# and without -CAfile it reports "unable to get local issuer certificate" after
# printing a correct chain. netscli's first signed release failed exactly that
# way. -TSA-CAfile is the same for the timestamp chain.
CA_BUNDLE=""
for candidate in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt; do
  [[ -f "$candidate" ]] && { CA_BUNDLE="$candidate"; break; }
done
if [[ -z "$CA_BUNDLE" ]]; then
  echo "ERROR: no system CA bundle found; cannot verify the signatures produced." >&2
  exit 1
fi

echo
echo "Verifying signatures against ${CA_BUNDLE}"
failed=0
for f in "${exes[@]}"; do
  if osslsigncode verify -CAfile "$CA_BUNDLE" -TSA-CAfile "$CA_BUNDLE" "$f" >/dev/null 2>&1; then
    printf '  %-24s signed\n' "$(basename "$f")"
  else
    printf '  %-24s NO VALID SIGNATURE\n' "$(basename "$f")"
    osslsigncode verify -CAfile "$CA_BUNDLE" -TSA-CAfile "$CA_BUNDLE" "$f" 2>&1 | sed 's/^/      /' >&2 || true
    failed=1
  fi
done

if [[ "$failed" -ne 0 ]]; then
  echo "ERROR: at least one executable came back without a valid signature." >&2
  exit 1
fi
echo "Signed ${#exes[@]} executable(s)."
