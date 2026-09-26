#!/bin/sh
# Regression test for install.sh's download and checksum step.
#
# install.sh wrote the download straight over $HOME/.nvx/bin/nvx and deleted it
# when the checksum did not match, so an upgrade that failed its check left no
# nvx at all where a working one had been. Nothing tested the download half of
# the installer, so this runs it for real against a stub curl that serves local
# files, with HOME pointed at a scratch directory. The same harness covers the
# checks install.sh makes before downloading, and what it tells a fish user.
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

# uname -m answers $FAKE_ARCH when it is set, and the real uname otherwise.
cat > "$WORK/stub/uname" <<STUB
#!/bin/sh
if [ "\$1" = "-m" ] && [ -n "\${FAKE_ARCH:-}" ]; then echo "\$FAKE_ARCH"; exit 0; fi
exec "$(command -v uname)" "\$@"
STUB
chmod +x "$WORK/stub/uname"

# A PATH with the tools install.sh uses and no sha256sum or shasum. Wrappers
# rather than links, so each tool still finds its own libraries.
NOHASH="$WORK/nohash"
mkdir -p "$NOHASH"
for tool in sh mkdir uname tr rm awk mv chmod cp cat grep touch basename; do
    printf '#!/bin/sh\nexec "%s" "$@"\n' "$(command -v "$tool")" > "$NOHASH/$tool"
    chmod +x "$NOHASH/$tool"
done

# run <case name> [env assignments...]: a fresh HOME holding a previous nvx.
# The assignments come last so a case can replace PATH or SHELL.
run() {
    name=$1; shift
    export HOME="$WORK/home-$name"
    mkdir -p "$HOME/.nvx/bin"
    printf 'PREVIOUS' > "$HOME/.nvx/bin/nvx"
    tr -d '\r' < "$ROOT/install.sh" > "$WORK/install.sh"
    if env PATH="$WORK/stub:$PATH" SHELL=/bin/sh "$@" sh "$WORK/install.sh" > "$WORK/out-$name" 2>&1; then
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

# The three cases below put the checksum back, so each one fails, if it does,
# for the reason it names.
echo "$(sha "$SERVE/bin")  nvx" > "$SERVE/sums"

echo "An unsupported CPU is refused rather than given the amd64 binary:"
run arch FAKE_ARCH=armv7l
[ "$status" -ne 0 ]; check "fails" $?
grep -q "armv7l" "$WORK/out-arch"; check "names the architecture" $?
[ "$(cat "$HOME/.nvx/bin/nvx")" = "PREVIOUS" ]; check "previous nvx untouched" $?

echo "No hashing tool is reported as that, not as a checksum mismatch:"
run nohash PATH="$WORK/stub:$NOHASH"
[ "$status" -ne 0 ]; check "fails" $?
grep -q "sha256sum" "$WORK/out-nohash"; check "names the missing tool" $?
! grep -q "Checksum verification failed" "$WORK/out-nohash"; check "does not claim a mismatch" $?
[ "$(cat "$HOME/.nvx/bin/nvx")" = "PREVIOUS" ]; check "previous nvx untouched" $?

echo "fish gets the manual step, and no POSIX line in a file it never reads:"
run fish SHELL=/usr/bin/fish
check "succeeds" "$status"
[ ! -e "$HOME/.profile" ]; check "no ~/.profile written" $?
grep -q "fish_add_path" "$WORK/out-fish"; check "prints the fish step" $?
! grep -q "profile has been updated" "$WORK/out-fish"; check "does not claim a profile was updated" $?

if [ "$fail" -ne 0 ]; then
    echo "install.sh download checks failed" >&2
    exit 1
fi
echo "install.sh download checks passed."
