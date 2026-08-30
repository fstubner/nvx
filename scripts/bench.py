#!/usr/bin/env python3
"""Measure nvx shim dispatch overhead versus running the runtime directly.

Cross-platform (uses time.perf_counter; macOS `date` lacks sub-second precision).
Builds nothing — point it at an existing nvx binary.

Usage:
    python3 scripts/bench.py [--nvx ./nvx] [--runs 40] [--runtime node]

Reports the median wall time for `node -e 0` vs the same command through the nvx
shim (with isolation disabled, so it measures dispatch + resolution overhead,
not the sandbox). The bin-resolve cache is warmed first so the number reflects
steady-state, which is what a developer actually experiences.
"""
import argparse
import os
import shutil
import statistics
import subprocess
import sys
import tempfile
import time


def bench(args, env, runs):
    samples = []
    for _ in range(runs):
        start = time.perf_counter()
        subprocess.run(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
        samples.append((time.perf_counter() - start) * 1000.0)
    return statistics.median(samples)


def resolve_real_runtime(name):
    """Return the real runtime binary, never the nvx shim that shadows it.

    On any machine where nvx is installed, `node` on PATH IS an nvx shim -- that
    is the whole point of it. Taking the baseline from shutil.which() therefore
    measured nvx against nvx and subtracted one from the other: run as README
    documents it, this printed an overhead of -50.4 ms. A negative number for a
    quantity that cannot be negative is the tell, and it survived because nobody
    reruns a benchmark whose figure is already written down.

    The binary is asked where it actually lives rather than guessed at from PATH
    order or directory names. Through a shim that answer comes back from the real
    runtime the shim dispatched to, which is exactly the thing to compare against.
    """
    found = shutil.which(name)
    if not found:
        sys.exit(f"{name} not found on PATH")
    try:
        out = subprocess.run(
            [found, "-p", "process.execPath"],
            capture_output=True, text=True, timeout=120,
        )
        real = out.stdout.strip()
    except (OSError, subprocess.SubprocessError):
        real = ""
    if not real or not os.path.exists(real):
        # Not a Node-like runtime, or it would not say. Fall back rather than
        # fail, but do not let the number be read as authoritative.
        print(f"warning: could not confirm {found} is the real runtime and not an "
              f"nvx shim; a negative overhead below means it was a shim.",
              file=sys.stderr)
        return found
    if os.path.normcase(os.path.realpath(real)) != os.path.normcase(os.path.realpath(found)):
        print(f"note: {found} is a shim; benchmarking against {real}", file=sys.stderr)
    return real


def resolve_nvx(given):
    """Return the nvx binary to benchmark, defaulting to the right name per OS.

    The default was "./nvx" on every platform, so running this the way README
    documents it -- `python scripts/bench.py`, no arguments -- could not work on
    Windows, where the build is nvx.exe. It failed one of two ways: "nvx binary
    not found", or, in a tree that also holds a macOS build named `nvx`, an
    unhandled OSError as Windows refused to execute a Mach-O file.

    An explicit --nvx is used exactly as given, so pointing it at an unusual
    build still fails loudly rather than silently benchmarking something else.
    """
    if given is not None:
        path = os.path.abspath(given)
        if not os.path.exists(path):
            sys.exit(f"nvx binary not found at {path}")
        return path

    names = ["nvx.exe", "nvx"] if os.name == "nt" else ["nvx", "nvx.exe"]
    for name in names:
        path = os.path.abspath(name)
        if os.path.exists(path):
            return path
    sys.exit(
        f"no nvx binary here (looked for {' and '.join(names)} in {os.getcwd()}).\n"
        f"Build one first:  go build -o {names[0]} ."
    )


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--nvx", default=None, help="path to the nvx binary (default: ./nvx.exe on Windows, ./nvx elsewhere)")
    ap.add_argument("--runs", type=int, default=40)
    ap.add_argument("--runtime", default="node", help="runtime binary to compare against")
    opts = ap.parse_args()

    runtime_bin = resolve_real_runtime(opts.runtime)
    nvx = resolve_nvx(opts.nvx)

    home = tempfile.mkdtemp(prefix="nvx-bench-")
    with open(os.path.join(home, "policy.json"), "w") as f:
        f.write('{"isolation":{"enabled":false}}')
    env = dict(os.environ, NVX_HOME=home)

    shim = [nvx, "shim", opts.runtime, "--no-sandbox", "-e", "0"]
    subprocess.run(shim, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)  # warm cache

    raw = bench([runtime_bin, "-e", "0"], env, opts.runs)
    wrapped = bench(shim, env, opts.runs)
    print(f"raw {opts.runtime} -e 0            : {raw:7.1f} ms (median of {opts.runs})")
    print(f"nvx shim {opts.runtime} (warm)     : {wrapped:7.1f} ms (median of {opts.runs})")
    print(f"=> nvx dispatch overhead    : {wrapped - raw:7.1f} ms")


if __name__ == "__main__":
    main()
