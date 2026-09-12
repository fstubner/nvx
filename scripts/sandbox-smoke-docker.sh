#!/usr/bin/env bash
# Docker provider smoke test — run after go build
#
# The docker provider had no automated coverage of any kind: no test called its
# launch function, and no CI step or script mentioned docker. What was tested
# was the argument list it builds, which is not the same as the container that
# results from it -- the mounts, the dropped capabilities and `--network none`
# were asserted as strings and never once observed to do anything. Three sibling
# providers were retired for being in exactly this position; this one works, so
# it gets a test instead.
#
# Asserts, inside a real container launched by nvx: that the command ran in a
# container at all, that the project is mounted and writable both ways, that the
# rest of the host filesystem is not there, that what it writes belongs to the
# user rather than to root, and that offline mode denies an outbound connection.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ./cmd/nvx" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  # Linux only, like the native smoke next to it. The host-filesystem assertion
  # below reads an absolute host path from inside the container, which is a real
  # question on Linux and a meaningless one where the host paths do not exist in
  # a Linux container's namespace anyway.
  echo "Linux-only smoke test; skipping." >&2
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not installed; skipping Docker provider smoke." >&2
  exit 0
fi
if ! docker info >/dev/null 2>&1; then
  echo "the docker daemon is not responding; skipping Docker provider smoke." >&2
  exit 0
fi

# nvx names the image from the Node version the session is using, so this is the
# tag it will ask for. A major tag rather than an exact version: an exact one
# ties the run to Docker Hub having published that release's tag yet, which is a
# way for this to fail for a reason that has nothing to do with nvx. Overridable
# so a developer can point it at an image already on the machine instead of
# pulling several hundred megabytes to run the script once.
NODE_VERSION="${NVX_SMOKE_NODE_VERSION:-22}"
IMAGE="node:${NODE_VERSION}"

echo "Fetching ${IMAGE} (the image nvx will ask docker for)..."
if ! docker pull -q "$IMAGE" >/dev/null 2>&1; then
  echo "could not pull ${IMAGE}" >&2
  exit 1
fi

# Under $HOME, not the system temp directory. The daemon has to be able to see
# the path it is asked to bind-mount, and its /tmp is often not the shell's:
# docker.service runs with PrivateTmp on many distributions, and Docker Desktop's
# daemon lives outside the WSL distro entirely. Either way `docker run -v` on a
# /tmp path silently mounts an empty directory instead of the project, and the
# command fails with the runtime reporting the script it was given is missing --
# which is what this script did on its first CI run.
PROJ="$(mktemp -d "$HOME/nvx-docker-smoke.XXXXXX")"
HOST_SECRET="$HOME/nvx-docker-smoke-host-probe.txt"
trap 'rm -rf "$PROJ" "$HOST_SECRET"' EXIT
echo "host-only content" > "$HOST_SECRET"

export NVX_HOME="$PROJ/nvxhome"
mkdir -p "$NVX_HOME"
# Docker is the one provider that supplies its own runtime: node comes from the
# image, so there is nothing to install. What nvx needs is the version the
# session is on, which it reads from PATH exactly as `nvx use` sets it -- hence
# the directory rather than an install.
mkdir -p "$NVX_HOME/versions/node/v${NODE_VERSION}/bin"
export PATH="$NVX_HOME/versions/node/v${NODE_VERSION}/bin:$PATH"

# offline, not the default proxy mode: docker does not enforce an allowlist, so
# nvx refuses proxy mode under it. offline and loopback are the two it does
# enforce, and offline is the one with something to observe.
cat > "$NVX_HOME/policy.json" <<JSON
{"isolation":{"enabled":true,"filesystem":{"provider":"docker"},"network":{"mode":"offline"}}}
JSON

cd "$PROJ"
cat > probe.js <<'JS'
const fs = require('fs');
const net = require('net');
const out = [];
out.push('IN_CONTAINER=' + (fs.existsSync('/.dockerenv') ? 'YES' : 'NO'));
out.push('CWD=' + process.cwd());
try { fs.writeFileSync('wrote-inside.txt', 'ok'); out.push('WRITE_PROJECT=ALLOWED'); }
catch { out.push('WRITE_PROJECT=DENIED'); }
try { fs.readFileSync(process.argv[2]); out.push('READ_HOST=ALLOWED'); }
catch { out.push('READ_HOST=DENIED'); }
require('dns').lookup('example.com', (err) => {
  out.push('DNS=' + (err ? 'DENIED' : 'ALLOWED'));
  const s = net.connect({ host: '1.1.1.1', port: 443 });
  const done = (verdict) => { out.push('EGRESS=' + verdict); console.log(out.join('\n')); process.exit(0); };
  s.setTimeout(5000);
  s.on('connect', () => done('ALLOWED'));
  s.on('error', () => done('DENIED'));
  s.on('timeout', () => done('TIMEOUT'));
});
JS

# Preflight: the daemon can see the project. Without this the failure surfaces as
# the runtime not finding its own script, which reads as an nvx bug and is not
# one. Skipping rather than failing, because a developer whose daemon cannot
# reach this path has an environment fact, not a regression -- and the CI step
# treats a run that does not reach the end as a failure regardless.
if ! docker run --rm --user "$(id -u):$(id -g)" -v "$PROJ:/app" -w /app "$IMAGE" node -e "require('fs').accessSync('probe.js')" >/dev/null 2>&1; then
  echo "this docker daemon cannot bind-mount $PROJ (it sees an empty directory)," >&2
  echo "so the provider cannot be exercised here. Skipping Docker provider smoke." >&2
  exit 0
fi

echo "Running a contained probe through the docker provider..."
# stderr kept, and the exit code checked here rather than left to set -e: when
# the run fails there is nothing else to go on, and a bare "exited 1" in a CI
# log is the shape of failure that takes an afternoon to reproduce.
set +e
REPORT="$("$NVX" -y --strict shim node probe.js "$HOST_SECRET" 2>&1)"
RC=$?
set -e
echo "--- contained run reported ---"
echo "$REPORT"
echo "------------------------------"
if [[ $RC -ne 0 ]]; then
  echo "the contained run through the docker provider failed (exit $RC)" >&2
  exit 1
fi

expect() {
  if ! grep -qx "$1" <<<"$REPORT"; then
    echo "expected the contained process to report $1" >&2
    exit 1
  fi
}

# It really was a container, and the project really is where the command runs.
expect "IN_CONTAINER=YES"
expect "CWD=/app"
# The mount carries writes both ways: allowed inside, and visible on the host.
expect "WRITE_PROJECT=ALLOWED"
if [[ ! -f "$PROJ/wrote-inside.txt" ]]; then
  echo "the project is mounted, but a write inside it never reached the host" >&2
  exit 1
fi
# And it belongs to the person who ran nvx. A container writing as root through a
# bind mount leaves files in the project that its owner cannot replace, which is
# one of the things the systemd-nspawn provider was retired for; the same trap
# is one flag away in any docker launch.
OWNER="$(stat -c %u "$PROJ/wrote-inside.txt")"
if [[ "$OWNER" != "$(id -u)" ]]; then
  echo "a file the contained process created is owned by uid $OWNER, not by you ($(id -u))" >&2
  exit 1
fi
# And nothing else of the host is there. The file exists and is readable to this
# user outside the container, so a DENIED here is the mount boundary and not a
# missing file.
if [[ ! -r "$HOST_SECRET" ]]; then
  echo "the host probe file is unreadable outside the container; the READ_HOST assertion would be meaningless" >&2
  exit 1
fi
expect "READ_HOST=DENIED"
# offline means offline. The connection is what is asserted; the name lookup is
# reported and deliberately NOT asserted, because it could not be shown to
# discriminate: with the policy flipped to an open network the same probe still
# reported DNS=DENIED, so asserting it would be asserting something never
# observed to fail. EGRESS did flip to ALLOWED in that run, which is what makes
# it worth having.
expect "EGRESS=DENIED"

# Phase 2: an actual install, which is what anyone reaches for this provider to
# do and the one thing that had never been tried. It failed the first time it
# was: the container stopped running as root (correctly), the scrubbed
# environment left it with no HOME, and npm resolved its cache to / and died
# with EACCES. Nothing in the argument-level tests could have shown that.
#
# open, not offline: the registry has to be reachable, and docker cannot enforce
# the allowlist proxy mode, so this is the only network mode an install can use
# under this provider. Worth seeing plainly -- an install under docker is
# unfiltered egress.
cat > "$NVX_HOME/policy.json" <<JSON
{"isolation":{"enabled":true,"filesystem":{"provider":"docker"},"network":{"mode":"open"}}}
JSON
printf '%s' '{"name":"probe","version":"1.0.0","dependencies":{"ms":"2.1.3"}}' > package.json

echo "Installing a package through the docker provider..."
set +e
INSTALL="$("$NVX" -y --strict shim npm install 2>&1)"
IRC=$?
set -e
if [[ $IRC -ne 0 ]]; then
  echo "$INSTALL" >&2
  echo "npm install through the docker provider failed (exit $IRC)" >&2
  exit 1
fi
if [[ ! -f node_modules/ms/package.json ]]; then
  echo "$INSTALL" >&2
  echo "npm install reported success but installed nothing" >&2
  exit 1
fi
MODOWNER="$(stat -c %u node_modules/ms/package.json)"
if [[ "$MODOWNER" != "$(id -u)" ]]; then
  echo "installed files are owned by uid $MODOWNER, not by you ($(id -u))" >&2
  exit 1
fi

echo "Docker sandbox smoke passed: ran in a container, project mounted both ways, host filesystem absent, egress denied, and npm install works and leaves files you own."
