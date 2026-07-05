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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--nvx", default="./nvx", help="path to the nvx binary")
    ap.add_argument("--runs", type=int, default=40)
    ap.add_argument("--runtime", default="node", help="runtime binary to compare against")
    opts = ap.parse_args()

    runtime_bin = shutil.which(opts.runtime)
    if not runtime_bin:
        sys.exit(f"{opts.runtime} not found on PATH")
    nvx = os.path.abspath(opts.nvx)
    if not os.path.exists(nvx):
        sys.exit(f"nvx binary not found at {nvx}")

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
