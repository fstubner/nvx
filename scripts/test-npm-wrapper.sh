#!/bin/sh
# Regression test for npm/nvx/bin/nvx.js's exit status.
#
# The wrapper runs the platform binary and exits with its status. When a
# signal ended the binary it exited 1, so a caller could not tell Ctrl-C from
# a failure. A shell reports 128+n, and so should the wrapper.
#
# Builds the node_modules layout npm would, with a stub binary, so nothing is
# installed. Linux and macOS only: Windows has no signals to test.
#
# Run from the repo root: sh scripts/test-npm-wrapper.sh
set -e

if [ ! -f npm/nvx/bin/nvx.js ]; then
    cd "$(dirname "$0")/.." || exit 1
fi
ROOT=$(pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

plat=$(node -p 'process.platform + "-" + process.arch')
wrapper="$WORK/node_modules/@fstubner/nvx/bin/nvx.js"
binpkg="$WORK/node_modules/@fstubner/nvx-$plat"
mkdir -p "$(dirname "$wrapper")" "$binpkg/bin"
cp "$ROOT/npm/nvx/bin/nvx.js" "$wrapper"
printf '{"name":"@fstubner/nvx-%s"}\n' "$plat" > "$binpkg/package.json"
cat > "$binpkg/bin/nvx" <<'STUB'
#!/bin/sh
case "$1" in
    exit) exit "$2" ;;
    signal) kill -"$2" $$ ;;
esac
STUB
chmod +x "$binpkg/bin/nvx"

set +e
fail=0
expect() { # want, args...
    want=$1; shift
    node "$wrapper" "$@"
    got=$?
    if [ "$got" -eq "$want" ]; then
        echo "  ok   $* -> $got"
    else
        echo "  FAIL $* -> $got, want $want"
        fail=1
    fi
}

expect 0 exit 0
expect 3 exit 3
expect 143 signal TERM
expect 137 signal KILL

if [ "$fail" -ne 0 ]; then
    echo "npm wrapper exit checks failed" >&2
    exit 1
fi
echo "npm wrapper exit checks passed."
