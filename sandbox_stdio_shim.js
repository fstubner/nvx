'use strict';
// Loaded into every contained node process on Windows via NODE_OPTIONS --require.
//
// A process inside an AppContainer cannot create a named pipe: CreateNamedPipeW
// returns ERROR_ACCESS_DENIED, which libuv reports as EADDRINUSE from uv_pipe_bind.
// Windows implements piped child stdio with named pipes, so any contained code that
// captures a subprocess's output blocks forever inside libuv before the child even
// exists. esbuild's postinstall does exactly that, which is why `npm install
// esbuild` used to hang indefinitely with no error.
//
// File descriptors are not restricted -- only pipes are. So for the synchronous
// capture APIs, whose entire contract is "run it, give me all the output at the
// end", a temp file is an exact substitute: the caller cannot observe the
// difference, because it never sees the stream either way.
//
// Only the sync APIs are patched. Async spawn() with stdio:'pipe' is a genuine
// stream that a file cannot stand in for, and it stays broken -- see the Known
// limitations entry. Nothing here weakens containment: it changes how a contained
// process talks to its own children, and the temp files live in the guest home.
//
// Every patch falls back to the original function if anything at all goes wrong,
// because this file is injected into every node process in the sandbox and must
// never be the reason one fails.

try {
  const cp = require('child_process');
  const fs = require('fs');
  const os = require('os');
  const path = require('path');

  const realSpawnSync = cp.spawnSync;
  const realExecFileSync = cp.execFileSync;
  const realExecSync = cp.execSync;

  // Slots the caller wants captured. Both the default (undefined) and an explicit
  // 'pipe' mean capture; 'inherit', 'ignore' and raw fds are left alone, since none
  // of them create a pipe.
  function slot(stdio, i) {
    if (stdio === undefined || stdio === null) return 'pipe';
    if (typeof stdio === 'string') return stdio;
    if (Array.isArray(stdio)) {
      const v = stdio[i];
      return v === undefined || v === null ? 'pipe' : v;
    }
    return stdio;
  }

  function needsSubstitution(options) {
    const stdio = options ? options.stdio : undefined;
    return slot(stdio, 1) === 'pipe' || slot(stdio, 2) === 'pipe';
  }

  // A scratch directory inside the guest home. os.tmpdir() is the AppContainer's
  // redirected temp, which nvx creates.
  function scratch() {
    return fs.mkdtempSync(path.join(os.tmpdir(), 'nvx-cap-'));
  }

  function decode(buf, options) {
    const enc = options && options.encoding;
    if (!enc || enc === 'buffer') return buf;
    return buf.toString(enc);
  }

  // Builds a stdio array of real file descriptors, plus the paths to read back.
  function openCapture(dir, options) {
    const stdio = options ? options.stdio : undefined;
    const outPath = path.join(dir, 'stdout');
    const errPath = path.join(dir, 'stderr');
    const fds = [];
    const close = [];

    // stdin: a file holding options.input, or an empty file so the child reads EOF
    // rather than inheriting the parent's stdin.
    const inSlot = slot(stdio, 0);
    if (inSlot === 'pipe') {
      const inPath = path.join(dir, 'stdin');
      let data = options && options.input;
      if (data === undefined || data === null) data = '';
      fs.writeFileSync(inPath, data);
      const fd = fs.openSync(inPath, 'r');
      fds[0] = fd;
      close.push(fd);
    } else {
      fds[0] = inSlot;
    }

    for (const [i, p] of [[1, outPath], [2, errPath]]) {
      if (slot(stdio, i) === 'pipe') {
        const fd = fs.openSync(p, 'w');
        fds[i] = fd;
        close.push(fd);
      } else {
        fds[i] = slot(stdio, i);
      }
    }

    return { fds, outPath, errPath, close, captured: {
      stdout: slot(stdio, 1) === 'pipe',
      stderr: slot(stdio, 2) === 'pipe',
    } };
  }

  function readBack(cap, options) {
    const out = cap.captured.stdout ? fs.readFileSync(cap.outPath) : null;
    const err = cap.captured.stderr ? fs.readFileSync(cap.errPath) : null;
    return {
      stdout: out === null ? null : decode(out, options),
      stderr: err === null ? null : decode(err, options),
    };
  }

  function cleanup(dir, cap) {
    for (const fd of cap.close) {
      try { fs.closeSync(fd); } catch (e) { /* already closed by the child */ }
    }
    try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e) { /* best effort */ }
  }

  // options.input is consumed by our stdin file; leaving it set would make the real
  // call try to build a pipe for it anyway.
  function withCaptureStdio(options, fds) {
    const next = Object.assign({}, options, { stdio: fds });
    delete next.input;
    return next;
  }

  cp.spawnSync = function spawnSync(file, args, options) {
    // spawnSync(file, options) is legal too.
    if (!Array.isArray(args) && args !== undefined && args !== null && options === undefined) {
      options = args;
      args = undefined;
    }
    if (!needsSubstitution(options)) return realSpawnSync(file, args, options);

    let dir, cap;
    try {
      dir = scratch();
      cap = openCapture(dir, options);
    } catch (e) {
      if (dir) { try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e2) {} }
      return realSpawnSync(file, args, options);
    }
    try {
      const r = realSpawnSync(file, args, withCaptureStdio(options, cap.fds));
      for (const fd of cap.close) { try { fs.closeSync(fd); } catch (e) {} }
      cap.close.length = 0;
      const got = readBack(cap, options);
      r.stdout = got.stdout;
      r.stderr = got.stderr;
      r.output = [null, got.stdout, got.stderr];
      return r;
    } finally {
      cleanup(dir, cap);
    }
  };

  // execFileSync and execSync are NOT reimplemented. They call node's internal
  // spawnSync, not the export patched above, so patching that alone would not reach
  // them -- but handing them a stdio array of real fds makes that internal call
  // pipeless, and node's own argument normalization and shell quoting stay in
  // charge. Only the return value has to be rebuilt, because node reports null for
  // a slot it did not itself pipe.
  function patchExec(real, name) {
    return function (...callArgs) {
      let optIndex = -1;
      for (let i = callArgs.length - 1; i >= 0; i--) {
        const a = callArgs[i];
        if (a && typeof a === 'object' && !Array.isArray(a)) { optIndex = i; break; }
      }
      const options = optIndex === -1 ? undefined : callArgs[optIndex];
      if (!needsSubstitution(options)) return real.apply(cp, callArgs);

      // execFileSync and execSync echo the child's stderr to the parent when the
      // caller did not pass stdio of its own -- node checks exactly that, so
      // injecting stdio below silently turns it off. An install script's warnings
      // would vanish from the terminal. Reproduce it after reading the files back.
      const inheritStderr = !(options && options.stdio);

      let dir, cap;
      try {
        dir = scratch();
        cap = openCapture(dir, options);
      } catch (e) {
        if (dir) { try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e2) {} }
        return real.apply(cp, callArgs);
      }

      const next = callArgs.slice();
      if (optIndex === -1) next.push(withCaptureStdio(undefined, cap.fds));
      else next[optIndex] = withCaptureStdio(options, cap.fds);

      try {
        let thrown = null;
        let ret;
        try {
          ret = real.apply(cp, next);
        } catch (e) {
          thrown = e;
        }
        for (const fd of cap.close) { try { fs.closeSync(fd); } catch (e) {} }
        cap.close.length = 0;
        const got = readBack(cap, options);
        if (inheritStderr && got.stderr !== null && got.stderr.length) {
          try { process.stderr.write(got.stderr); } catch (e) { /* closed stderr */ }
        }
        if (thrown) {
          // node builds this error before it can see our files; fill it in so a
          // caller inspecting err.stderr gets what it would have got with pipes.
          try {
            thrown.stdout = got.stdout;
            thrown.stderr = got.stderr;
            thrown.output = [null, got.stdout, got.stderr];
          } catch (e) { /* frozen error object */ }
          throw thrown;
        }
        // execFileSync/execSync return stdout itself.
        return got.stdout === null ? ret : got.stdout;
      } finally {
        cleanup(dir, cap);
      }
    };
  }

  cp.execFileSync = patchExec(realExecFileSync, 'execFileSync');
  cp.execSync = patchExec(realExecSync, 'execSync');

  // ---- async spawn: a stream, which a file cannot stand in for --------------
  //
  // The trick above does not extend here. What does work is that nvx pre-creates
  // named pipes outside the container and this process only OPENS them: creating
  // one is what an AppContainer is refused, opening one it is not.
  //
  // Per captured stream nvx provides a pair. The 'child' pipe's descriptor is
  // handed to the child as its stdout/stderr; the 'node' pipe carries the same
  // bytes back here. Two pipes because a named pipe joins a server to a client,
  // so the child and this process -- both clients -- cannot reach each other
  // directly, and nvx pumps between them.
  //
  // Every failure path falls back to the unpatched spawn, which restores the old
  // behaviour rather than inventing a new one.
  const channelSpec = process.env.NVX_STDIO_CHANNELS;
  if (channelSpec) {
    const net = require('net');
    const realSpawn = cp.spawn;
    const free = channelSpec.split(';').filter(Boolean).map(function (pair) {
      const parts = pair.split('|');
      return { childPipe: parts[0], nodePipe: parts[1] };
    });
    // The reverse-direction pool, for writing to a child. Its own variable and
    // its own list: an input channel taken for an output stream would copy the
    // wrong way and wedge silently.
    const freeIn = (process.env.NVX_STDIN_CHANNELS || '').split(';').filter(Boolean)
      .map(function (pair) {
        const parts = pair.split('|');
        return { childPipe: parts[0], nodePipe: parts[1] };
      });

    let warnedFallback = false;
    function warnStdioFallbackOnce() {
      if (warnedFallback) return;
      warnedFallback = true;
      try {
        process.stderr.write(
          'nvx: more concurrent piped children than the sandbox can stream; ' +
          'their output arrives when each stream ends rather than as it is produced ' +
          '(read it via stdout events or the close event, not in an exit handler).\n');
      } catch (e) { /* stderr may be closed */ }
    }

    // The fallback when the channel pool is empty. Same substitution the
    // synchronous APIs use -- descriptors, which an AppContainer may create --
    // with the output replayed through a stream once the child has exited, so a
    // caller doing child.stdout.on('data') still receives it.
    function spawnThroughFiles(command, argv, opts, stdio, wanted) {
      const stream = require('stream');
      let dir;
      try {
        dir = scratch();
      } catch (e) {
        return realSpawn.call(cp, command, Array.isArray(argv) ? argv : [], opts);
      }

      const fds = [slot(stdio, 0), slot(stdio, 1), slot(stdio, 2)];
      const paths = {};
      const open = [];
      try {
        if (fds[0] === 'pipe') {
          const inPath = path.join(dir, 'stdin');
          fs.writeFileSync(inPath, '');
          fds[0] = fs.openSync(inPath, 'r');
          open.push(fds[0]);
        }
        for (const i of wanted) {
          const p = path.join(dir, i === 1 ? 'stdout' : 'stderr');
          fs.writeFileSync(p, '');
          fds[i] = fs.openSync(p, 'w');
          open.push(fds[i]);
          paths[i] = p;
        }
      } catch (e) {
        for (const fd of open) { try { fs.closeSync(fd); } catch (e2) {} }
        try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e2) {}
        return realSpawn.call(cp, command, Array.isArray(argv) ? argv : [], opts);
      }

      const child = realSpawn.call(cp, command, Array.isArray(argv) ? argv : [],
        Object.assign({}, opts || {}, { stdio: fds }));

      // Said out loud, once, because this path behaves differently from the
      // streamed one IN THE SAME PROCESS: the first children stream as they go,
      // and these deliver everything when the stream ends. A caller that reads
      // its accumulated buffer in the child's 'exit' handler sees it empty here
      // and full for the earlier children -- which looks like a flaky test
      // rather than a limit being crossed. Exit codes and totals are unaffected.
      warnStdioFallbackOnce();

      const replay = {};
      for (const i of wanted) {
        const s = new stream.PassThrough();
        s.on('error', function () {});
        replay[i] = s;
        if (i === 1) child.stdout = s; else child.stderr = s;
        try { child.stdio[i] = s; } catch (e) {}
      }

      child.once('close', function () {
        for (const fd of open) { try { fs.closeSync(fd); } catch (e) {} }
        for (const i of wanted) {
          let data = null;
          try { data = fs.readFileSync(paths[i]); } catch (e) {}
          try {
            if (data && data.length) replay[i].write(data);
            replay[i].end();
          } catch (e) {}
        }
        try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e) {}
      });
      return child;
    }

    cp.spawn = function nvxSpawn(command, args, options) {
      // spawn's signature lets args and options swap, so normalise before
      // reading stdio out of it.
      let opts = options;
      let argv = args;
      if (opts === undefined && args && !Array.isArray(args)) {
        opts = args;
        argv = [];
      }
      const stdio = opts ? opts.stdio : undefined;

      const wanted = [];
      if (slot(stdio, 1) === 'pipe') wanted.push(1);
      if (slot(stdio, 2) === 'pipe') wanted.push(2);
      if (!wanted.length) {
        return realSpawn.apply(cp, arguments);
      }

      // Out of channels is NOT a reason to hand this back to the real spawn:
      // that call blocks synchronously inside libuv and wedges the entire
      // process -- not just this child, and not recoverably, since even a timer
      // already set never fires. An acceptance review found the fifth
      // concurrent child doing exactly that.
      //
      // Files instead. The output arrives when the child exits rather than as
      // it is produced, which is a real loss for a progress spinner and no loss
      // at all for a command that finishes. Degrading beats hanging.
      if (free.length < wanted.length) {
        return spawnThroughFiles(command, argv, opts, stdio, wanted);
      }

      const taken = [];
      const fds = [slot(stdio, 0), slot(stdio, 1), slot(stdio, 2)];

      // stdin has to be substituted too, and forgetting it wasted an hour:
      // leaving slot 0 as 'pipe' means node still creates a pipe for it, which
      // is the exact call an AppContainer refuses. Every stdout/stderr channel
      // in the world does not help if the process hangs on stdin first.
      //
      // A reverse channel when one is free, so child.stdin is a real stream.
      // It was an empty file until 2026-09-04, which made child.stdin null and
      // was documented as "a tool that feeds its child input needs
      // --no-sandbox". That undersold it: esbuild's service is a child driven
      // over stdin, vite runs on esbuild and vitest runs on vite, so `npx vitest
      // run` on an already-installed binary hung indefinitely -- measured at
      // over 120s against 4.1s uncontained -- with no error to explain it.
      //
      // The empty file remains the fallback when the pool is exhausted: the old
      // limitation, not a new failure.
      let stdinDir = null;
      let stdinTaken = null;
      if (fds[0] === 'pipe') {
        const inCh = freeIn.shift();
        if (inCh) {
          try {
            // 'r+' on both: these pipes are duplex and a one-way open is
            // refused. The child reads childPipe as its fd 0; this process
            // writes nodePipe, and nvx pumps one into the other.
            const childFd = fs.openSync(inCh.childPipe, 'r+');
            const writeFd = fs.openSync(inCh.nodePipe, 'r+');
            fds[0] = childFd;
            stdinTaken = { ch: inCh, childFd: childFd, writeFd: writeFd };
          } catch (e) {
            if (stdinTaken) {
              try { fs.closeSync(stdinTaken.childFd); } catch (e2) {}
              try { fs.closeSync(stdinTaken.writeFd); } catch (e2) {}
            }
            stdinTaken = null;
            freeIn.push(inCh);
          }
        }
        if (!stdinTaken) {
          try {
            stdinDir = scratch();
            const emptyPath = path.join(stdinDir, 'stdin');
            fs.writeFileSync(emptyPath, '');
            fds[0] = fs.openSync(emptyPath, 'r');
          } catch (e) {
            return realSpawn.apply(cp, arguments);
          }
        }
      }

      try {
        for (const i of wanted) {
          const ch = free.shift();
          // 'r+' rather than 'w': these pipes are duplex, and opening one
          // write-only is refused.
          const writeFd = fs.openSync(ch.childPipe, 'r+');
          const readFd = fs.openSync(ch.nodePipe, 'r+');
          fds[i] = writeFd;
          taken.push({ slot: i, ch: ch, writeFd: writeFd, readFd: readFd });
        }
      } catch (e) {
        for (const t of taken) {
          try { fs.closeSync(t.writeFd); } catch (e2) {}
          try { fs.closeSync(t.readFd); } catch (e2) {}
          free.push(t.ch);
        }
        return realSpawn.apply(cp, arguments);
      }

      const nextOpts = Object.assign({}, opts || {}, { stdio: fds });
      const child = realSpawn.call(cp, command, Array.isArray(argv) ? argv : [], nextOpts);

      // node reports null for a slot passed as a descriptor, so the readable
      // side is attached here. Anything doing child.stdout.on('data') or
      // .pipe() then sees an ordinary stream.
      for (const t of taken) {
        let stream;
        try {
          stream = new net.Socket({ fd: t.readFd, readable: true, writable: false });
        } catch (e) {
          stream = fs.createReadStream('', { fd: t.readFd });
        }
        // A stream with no error listener throws out of the event loop and
        // takes the whole contained process down -- which is exactly what
        // happened here with an EPIPE at end of stream. This shim is injected
        // into every node process in the sandbox and must never be the reason
        // one dies, so an error ends the stream instead of raising.
        try {
          stream.on('error', function () {
            try { stream.destroy(); } catch (e2) {}
          });
        } catch (e) {}

        if (t.slot === 1) child.stdout = stream;
        else child.stderr = stream;
        try { child.stdio[t.slot] = stream; } catch (e) {}
      }

      // The writable side, for the same reason: node reports null for a slot
      // passed as a descriptor, so a caller doing child.stdin.write() would see
      // null without this.
      if (stdinTaken) {
        let inStream;
        try {
          inStream = new net.Socket({ fd: stdinTaken.writeFd, readable: false, writable: true });
        } catch (e) {
          inStream = fs.createWriteStream('', { fd: stdinTaken.writeFd });
        }
        // Same rule as the readable side: this shim is in every contained node
        // process and must never be why one dies.
        try {
          inStream.on('error', function () {
            try { inStream.destroy(); } catch (e2) {}
          });
        } catch (e) {}
        child.stdin = inStream;
        try { child.stdio[0] = inStream; } catch (e) {}
      }

      // This process also holds the child's write end, and while it does the
      // reader never sees EOF: the stream would deliver every byte and then hang
      // forever, which is the bug being fixed wearing a disguise.
      child.once('spawn', function () {
        for (const t of taken) {
          try { fs.closeSync(t.writeFd); } catch (e) {}
        }
        if (typeof fds[0] === 'number') {
          try { fs.closeSync(fds[0]); } catch (e) {}
        }
      });
      child.once('close', function () {
        for (const t of taken) free.push(t.ch);
        if (stdinTaken) {
          // Destroy rather than close the fd directly: the socket owns it, and
          // closing underneath it raises EBADF on the next tick.
          try { child.stdin && child.stdin.destroy(); } catch (e) {}
          freeIn.push(stdinTaken.ch);
        }
      });
      return child;
    };
  }

  // An IPC channel is a named pipe libuv creates INSIDE the container, and an
  // AppContainer refuses to create one. Unlike stdout, stderr and stdin, this
  // one cannot be handed over ready-made: node's 'ipc' slot is not an ordinary
  // descriptor, and the parent half of the channel is built by node itself.
  //
  // So it hangs. Not slowly -- forever, inside the fork() call, before the child
  // exists and before anything is printed: measured with file markers, the line
  // before fork() is written and the line after it never is. `npx vitest run`
  // hung for over five minutes twice in the session that reported it, with no
  // output to suggest a cause, against 1.9s outside the sandbox.
  //
  // Failing here is strictly better than that. It is the same limitation either
  // way; this version says so, names the flag that works, and takes a second
  // instead of forever. When the channel is eventually brokered like the other
  // three, this goes away with it.
  const realFork = cp.fork;
  cp.fork = function () {
    const err = new Error(
      'nvx: child_process.fork() needs an IPC channel, which a Windows AppContainer ' +
      'cannot create, so this would hang rather than fail. Re-run with `nvx --no-sandbox` ' +
      '(vitest and other test runners fork worker processes by default).');
    err.code = 'ERR_NVX_IPC_UNSUPPORTED';
    throw err;
  };
  // Kept reachable: a caller that knows what it is doing, and a future in which
  // the channel is brokered, both need the real one.
  cp.fork.nvxRealFork = realFork;
} catch (e) {
  // Never break a contained process because of this shim.
}
