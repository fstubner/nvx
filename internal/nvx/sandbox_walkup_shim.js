'use strict';
// Loaded into every contained node process on Windows via NODE_OPTIONS --require.
//
// Tools walk up from a path to the drive root and stat each directory on the
// way. npm's own realpath does it for every tree it loads, and `npx` loads one
// from its cache under the sandbox's home, so the walk passes C:\Users and
// C:\ every time. An AppContainer can read a directory's attributes only when
// it holds an entry on that directory or may list its parent; on C:\Users and
// on a drive root it holds neither, so lstat fails with EPERM and npm gives up:
//
//   npm error Error: EPERM: operation not permitted, lstat 'C:\Users'
//
// Measured 2026-09-03 with no drive-root grant: `npm install` in a project on
// C: works, `npx -y cowsay hi` from the same project fails exactly like that.
// Until now the answer was an elevated `nvx setup` writing an entry on each
// drive root -- 22 minutes on one 5.6-million-entry volume, and an
// Administrator prompt for a permission the sandbox never uses for anything
// but this walk.
//
// The directories in question exist by construction: they are the ancestors
// of the sandbox's own working directory and of its home. Traversing them is
// already permitted (that is what the sandbox does to reach either place);
// only their attributes are hidden. So when a stat of such an ancestor fails
// with EPERM, this preload answers with the stats of a directory instead. It
// never invents a directory that could be absent, never touches a path outside
// those two chains, and never changes an answer the OS was willing to give.
//
// Nothing here weakens containment: the process learns that C:\Users is a
// directory, which it already knew from its own path. Every patch falls back
// to the original function if anything at all goes wrong, because this file is
// injected into every node process in the sandbox and must never be the reason
// one fails.

try {
  const fs = require('fs');
  const fsp = require('fs/promises');
  const path = require('path');

  // The chains whose ancestors may be answered for. Lower-cased and resolved,
  // because Windows paths compare that way and callers spell them every way.
  const chains = [];
  for (const p of [process.cwd(), process.env.USERPROFILE, process.env.HOME]) {
    if (typeof p === 'string' && p) {
      try { chains.push(path.resolve(p).toLowerCase()); } catch (e) { /* skip */ }
    }
  }

  function isCoveredAncestor(p) {
    let q;
    try { q = path.resolve(String(p)).toLowerCase(); } catch (e) { return false; }
    const withSep = q.endsWith(path.sep) ? q : q + path.sep;
    for (const chain of chains) {
      if (chain !== q && chain.startsWith(withSep)) return true;
    }
    return false;
  }

  function isPermissionError(e) {
    return !!e && (e.code === 'EPERM' || e.code === 'EACCES');
  }

  // Exported so the narrowness this file claims can be asserted rather than
  // merely stated. Loading via `--require` ignores module.exports; a test
  // requires the file directly and checks the two predicates against paths that
  // must be refused. Without this, forcing isCoveredAncestor to `return true` --
  // which makes the shim fabricate stats for ANY denied path anywhere, the exact
  // opposite of what the header promises -- left the whole suite green.
  if (typeof module === 'object' && module.exports) {
    module.exports.isCoveredAncestor = isCoveredAncestor;
    module.exports.isPermissionError = isPermissionError;
  }

  // A real directory's Stats, borrowed from one the sandbox can read, so the
  // answer has every method and field a caller might look at.
  let template = null;
  function directoryStats() {
    if (template) return template;
    for (const p of [process.cwd(), process.env.USERPROFILE, process.env.HOME]) {
      try {
        const st = fs.lstatSync(p);
        if (st.isDirectory()) { template = st; return st; }
      } catch (e) { /* try the next */ }
    }
    return null;
  }

  function wrapSync(orig) {
    return function (p, ...rest) {
      try {
        return orig.call(this, p, ...rest);
      } catch (e) {
        if (isPermissionError(e) && isCoveredAncestor(p)) {
          const st = directoryStats();
          if (st) return st;
        }
        throw e;
      }
    };
  }

  function wrapCallback(orig) {
    return function (p, ...rest) {
      const cb = typeof rest[rest.length - 1] === 'function' ? rest.pop() : null;
      if (!cb) return orig.call(this, p, ...rest);
      return orig.call(this, p, ...rest, (err, st) => {
        if (isPermissionError(err) && isCoveredAncestor(p)) {
          const fake = directoryStats();
          if (fake) return cb(null, fake);
        }
        cb(err, st);
      });
    };
  }

  function wrapPromise(orig) {
    return async function (p, ...rest) {
      try {
        return await orig.call(this, p, ...rest);
      } catch (e) {
        if (isPermissionError(e) && isCoveredAncestor(p)) {
          const st = directoryStats();
          if (st) return st;
        }
        throw e;
      }
    };
  }

  fs.lstatSync = wrapSync(fs.lstatSync);
  fs.statSync = wrapSync(fs.statSync);
  fs.lstat = wrapCallback(fs.lstat);
  fs.stat = wrapCallback(fs.stat);
  // require('fs/promises') and fs.promises are the same object.
  fsp.lstat = wrapPromise(fsp.lstat);
  fsp.stat = wrapPromise(fsp.stat);
} catch (e) {
  // Never the reason a contained process fails.
}
