#!/bin/sh
# Regression test for install.sh's download and checksum step.
#
# install.sh wrote the download straight over $HOME/.nvx/bin/nvx and deleted it
# when the checksum did not match, so an upgrade that failed its check left no
# nvx at all where a working one had been. Nothing tested the download half of
# the installer, so this runs it for real against a stub curl that serves local
# files, with HOME pointed at a scratch directory.
#
# Run from the repo root: sh scripts/test-install-download.sh
set -e

if [ ! -f install.sh ]; then
    cd "$(dirname "$0")/.." || exit 1
fi
ROOT=$(pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
# Checks below test conditions whose failure is the point; -e would abort on
# the first one instead of reporting it.
set +e
fail=0

sha() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# The stub serves $SERVE/bin for the binary and $SERVE/sums for its .sha256,
# and fails (as curl -f does on a 404) when the file is not there.
mkdir -p "$WORK/stub"
cat > "$WORK/stub/curl" <<'STUB'
#!/bin/sh
url=""; out=""
while [ $# -gt 0 ]; do
    case "$1" in
        -o) out="$2"; shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
case "$url" in
    *.sha256) src="$SERVE/sums" ;;
    *) src="$SERVE/bin" ;;
esac
[ -f "$src" ] || exit 22
cp "$src" "$out"
STUB
chmod +x "$WORK/stub/curl"

# run <case name> [env assignments...]: a fresh HOME holding a previous nvx.
run() {
    name=$1; shift
    export HOME="$WORK/home-$name"
    mkdir -p "$HOME/.nvx/bin"
    printf 'PREVIOUS' > "$HOME/.nvx/bin/nvx"
    tr -d '\r' < "$ROOT/install.sh" > "$WORK/install.sh"
    if env "$@" PATH="$WORK/stub:$PATH" SHELL=/bin/sh sh "$WORK/install.sh" > "$WORK/out-$name" 2>&1; then
        status=0
    else
        status=1
    fi
}

check() { # label, condition-result (0 = ok)
    if [ "$2" -eq 0 ]; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi
}

export SERVE="$WORK/serve"

echo "A matching checksum installs:"
mkdir -p "$SERVE"; printf 'NEW' > "$SERVE/bin"; echo "$(sha "$SERVE/bin")  nvx" > "$SERVE/sums"
run match
check "succeeds" "$status"
[ "$(cat "$HOME/.nvx/bin/nvx")" = "NEW" ]; check "nvx is the new binary" $?
[ ! -e "$HOME/.nvx/bin/nvx.download" ] && [ ! -e "$HOME/.nvx/bin/nvx.sha256" ]; check "no side files left" $?

echo "A mismatch is refused and leaves the previous nvx:"
echo "0000000000000000000000000000000000000000000000000000000000000000  nvx" > "$SERVE/sums"
run mismatch
[ "$status" -ne 0 ]; check "fails" $?
[ "$(cat "$HOME/.nvx/bin/nvx")" = "PREVIOUS" ]; check "previous nvx untouched" $?
[ ! -e "$HOME/.nvx/bin/nvx.download" ]; check "download removed" $?

echo "A missing checksum is refused by default:"
rm -f "$SERVE/sums"
run missing
[ "$status" -ne 0 ]; check "fails" $?
[ "$(cat "$HOME/.nvx/bin/nvx")" = "PREVIOUS" ]; check "previous nvx untouched" $?

echo "A missing checksum installs with NVX_INSECURE_SKIP_CHECKSUM=1:"
run skip NVX_INSECURE_SKIP_CHECKSUM=1
check "succeeds" "$status"
[ "$(cat "$HOME/.nvx/bin/nvx")" = "NEW" ]; check "nvx is the new binary" $?

if [ "$fail" -ne 0 ]; then
    echo "install.sh download checks failed" >&2
    exit 1
fi
echo "install.sh download checks passed."
