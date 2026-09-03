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


MARKER = "NVX_BENCH_RAN"


def verify_arm(name, args, marker_args, env):
    """Refuse to time a command that is not doing the work being measured.

    Every overhead figure this project ever published came from a command that
    failed. bench.py timed `nvx shim node --no-sandbox -e 0`, and nvx does not
    take its own flags from a wrapped command's arguments -- deliberately, so
    that `nvx npx tsc --strict` gives tsc its --strict. So --no-sandbox went to
    node, which answered `bad option: --no-sandbox` and exited 9. The shim arm
    was timing an argument-parsing failure against a real node run.

    Measured 2026-09-03 on Windows and on Linux in a container: exit 9, no
    script executed, on both. On Linux the failure is faster than starting node
    properly, so the script reported a NEGATIVE overhead; on Windows the extra
    process made it positive, which is why it looked plausible there for months
    and was published as ~3 ms Linux, ~4 ms macOS and 1-60 ms Windows.

    A timing harness that cannot tell success from failure will eventually
    publish one as the other. So each arm must exit 0 AND print a marker from
    inside the runtime before anything is timed.
    """
    out = subprocess.run(args, capture_output=True, text=True, env=env)
    if out.returncode != 0:
        sys.exit(f"the {name} command failed (exit {out.returncode}) and would have been timed as "
                 f"though it worked:\n  {' '.join(args)}\n{out.stdout}{out.stderr}")
    proof = subprocess.run(marker_args, capture_output=True, text=True, env=env)
    if MARKER not in proof.stdout:
        sys.exit(f"the {name} command did not run the runtime; nothing printed the marker:\n"
                 f"  {' '.join(marker_args)}\n{proof.stdout}{proof.stderr}")


def once(args, env):
    start = time.perf_counter()
    subprocess.run(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
    return (time.perf_counter() - start) * 1000.0


def paired_bench(raw_args, shim_args, env, runs, warmup=3):
    """Time both commands alternately and return the per-pair differences.

    Interleaved, and paired, because the previous design could not support the
    number it printed. It timed all the raw runs, then all the wrapped runs, and
    subtracted one median from the other. Process startup on Windows drifts with
    whatever else the machine is doing, so the two batches were sampled under
    different conditions and the drift landed entirely in the answer: measured
    2026-09-03 on one idle laptop, four consecutive invocations reported 140.0,
    147.8, 92.9 and 42.5 ms overhead, while the RAW baseline alone swung from
    65.3 to 141.8 ms between runs. A difference of two independently drifting
    medians is noise wearing a decimal point.

    Alternating within one loop puts both arms under the same conditions, and
    differencing each pair cancels the drift they share. What survives is the
    quantity being asked about: how much longer the same work takes through the
    shim.

    The first few pairs are discarded. Neither binary is warm on the first call
    -- the OS file cache, the loader, and nvx's own bin-resolve cache all cost
    something once -- and a developer's steady state is the thing being
    described.
    """
    deltas, raws = [], []
    for i in range(runs + warmup):
        raw = once(raw_args, env)
        wrapped = once(shim_args, env)
        if i >= warmup:
            deltas.append(wrapped - raw)
            raws.append(raw)
    return deltas, raws


def spread(samples):
    """Median and 10th/90th percentiles, so a reader sees the noise."""
    lo, hi = samples[0], samples[-1]
    if len(samples) >= 10:
        q = statistics.quantiles(samples, n=10)
        lo, hi = q[0], q[-1]
    else:
        lo, hi = min(samples), max(samples)
    return statistics.median(samples), lo, hi


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

    Existing is not enough; it has to RUN. This repo's own tree carries a
    gitignored macOS build named `nvx`, so on Windows with no nvx.exe the
    fallback picked a Mach-O file and the script died with an unhandled
    "OSError: [WinError 193] %1 is not a valid Win32 application" -- which is
    verbatim the failure this docstring already claimed to have fixed. Preferring
    the right NAME is not the same as checking the file is for this platform, and
    an acceptance pass found the difference by running it in this very tree.

    An explicit --nvx is checked the same way, so pointing it at a build for
    another OS fails with a sentence rather than a traceback.
    """
    def usable(path):
        """True if this binary can actually be executed here."""
        try:
            subprocess.run([path, "version"], stdout=subprocess.DEVNULL,
                           stderr=subprocess.DEVNULL, timeout=60)
            return True
        except OSError:
            return False
        except subprocess.SubprocessError:
            # It ran and misbehaved, which is not this function's problem.
            return True

    if given is not None:
        path = os.path.abspath(given)
        if not os.path.exists(path):
            sys.exit(f"nvx binary not found at {path}")
        if not usable(path):
            sys.exit(f"{path} exists but cannot be executed here; is it built for this OS?")
        return path

    names = ["nvx.exe", "nvx"] if os.name == "nt" else ["nvx", "nvx.exe"]
    found_unusable = []
    for name in names:
        path = os.path.abspath(name)
        if not os.path.exists(path):
            continue
        if usable(path):
            return path
        found_unusable.append(path)

    detail = ""
    if found_unusable:
        detail = ("\nFound " + ", ".join(found_unusable) +
                  ", but nothing there can be executed here; built for another OS?")
    sys.exit(
        f"no runnable nvx binary here (looked for {' and '.join(names)} in {os.getcwd()}).{detail}\n"
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

    # --no-sandbox goes BEFORE the subcommand. nvx reads its own flags only
    # there; anything after a wrapped command's name belongs to that command and
    # is passed through untouched. See verify_arm for what the other order cost.
    shim = [nvx, "--no-sandbox", "shim", opts.runtime, "-e", "0"]
    raw_cmd = [runtime_bin, "-e", "0"]

    marker = f"console.log('{MARKER}')"
    verify_arm("raw", raw_cmd, [runtime_bin, "-e", marker], env)
    verify_arm("nvx shim", shim, [nvx, "--no-sandbox", "shim", opts.runtime, "-e", marker], env)

    deltas, raws = paired_bench(raw_cmd, shim, env, opts.runs)
    d_med, d_lo, d_hi = spread(deltas)
    r_med, r_lo, r_hi = spread(raws)

    print(f"raw {opts.runtime} -e 0        : {r_med:7.1f} ms   (p10 {r_lo:.1f}, p90 {r_hi:.1f})")
    print(f"paired overhead        : {d_med:7.1f} ms   (p10 {d_lo:.1f}, p90 {d_hi:.1f})")
    print(f"                         {len(deltas)} pairs, alternated; each pair differenced")

    # A spread wider than the number itself means this machine cannot answer the
    # question today, and saying so is the point. The previous script printed a
    # single figure regardless, which is how a range nobody could reproduce came
    # to be published in README and PRODUCT.md.
    if d_hi - d_lo > abs(d_med):
        print("\nwarning: the spread here is wider than the median, so this run does not")
        print("support a single figure. Re-run on a quiet machine, or quote the spread.")


if __name__ == "__main__":
    main()
