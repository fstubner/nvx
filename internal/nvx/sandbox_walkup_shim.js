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
//
// The same ancestors hold one more thing a tool walks up to read: yarn classic
// reads a .yarnrc from every directory between the project and the drive root,
// and the sandbox refuses the one in the real home on purpose. See
// isHiddenRcFile.

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

  // yarn classic reads .yarnrc and .npmrc (and their .yml forms) from every
  // directory between the project and the drive root. Under the user's profile
  // that includes the real home, which the sandbox does not let a contained
  // process read, and yarn treats EPERM there as fatal where ENOENT means "no
  // file":
  //
  //   Error: EPERM: operation not permitted, open 'C:\Users\<name>\.yarnrc'
  //
  // Measured 2026-10-06 with yarn 1.22.22 under Node 22.23.3: the stack ends in
  // parseRcPaths, which skips ENOENT and EISDIR and rethrows everything else.
  // The same failure follows for ~/.npmrc once ~/.yarnrc is answered.
  // The sandbox hides those files on purpose, so to the contained process they
  // do not exist. A refused read of one therefore reports ENOENT. Nothing
  // becomes readable, only the error code changes. The rule covers only these
  // rc names directly inside an ancestor of the working directory or home,
  // the one place yarn looks that the sandbox hides. Any other refused read, of
  // any other name or place, keeps its EPERM.
  function isHiddenRcFile(p) {
    if (typeof p !== 'string') return false;
    if (!/^\.(yarn|npm)rc(\.yml)?$/i.test(path.basename(p))) return false;
    return isCoveredAncestor(path.dirname(p));
  }

  function enoentFor(p) {
    const err = new Error("ENOENT: no such file or directory, open '" + p + "'");
    err.errno = -4058;
    err.code = 'ENOENT';
    err.syscall = 'open';
    err.path = p;
    return err;
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
    module.exports.isHiddenRcFile = isHiddenRcFile;
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

  // The native realpath functions ask Windows for a handle's final path with
  // its drive letter (GetFinalPathNameByHandleW, VOLUME_NAME_DOS), and an
  // AppContainer is refused that on every path, the project directory
  // included. pnpm 10 resolves the project with fs.promises.realpath and stops:
  //
  //   EPERM: operation not permitted, realpath 'C:\...\project'
  //
  // Node's JavaScript realpath reaches the same answer by walking the path with
  // lstat and readlink, which the sandbox may do. Measured 2026-10-04 inside the
  // container with Node 22.23.3, on C: and on H: of a machine whose drive roots
  // `nvx setup` had granted: realpathSync.native and promises.realpath gave
  // EPERM on the working directory, the sandbox's home, C:\Users and C:\, and
  // realpathSync and the callback realpath returned the path for all of them.
  // So a refused native call is retried with the JavaScript one. It learns
  // nothing the process could not learn with lstat, and any other error, or a
  // failed retry, surfaces the original error.
  const jsRealpathSync = fs.realpathSync;
  const jsRealpath = fs.realpath;

  // Without that grant, Node's synchronous realpath still fails: it stats each
  // component through its internal binding, which the lstat patch above cannot
  // reach, and C:\Users and the drive root refuse it. The callback realpath
  // stats through fs.lstat and is answered. CI's runner, which has no grant,
  // showed exactly that split. This walk does what the synchronous one does
  // through fs.lstatSync and fs.readlinkSync, so the ancestors are answered.
  function walkRealpathSync(p) {
    if (p instanceof URL) p = require('url').fileURLToPath(p);
    let resolved = path.resolve(String(p));
    for (let links = 0; links <= 40; links++) {
      const root = path.parse(resolved).root;
      const parts = resolved.slice(root.length).split(path.sep).filter(Boolean);
      let current = root;
      let restarted = false;
      for (let i = 0; i < parts.length; i++) {
        const next = path.join(current, parts[i]);
        if (fs.lstatSync(next).isSymbolicLink()) {
          resolved = path.resolve(current, fs.readlinkSync(next), ...parts.slice(i + 1));
          restarted = true;
          break;
        }
        current = next;
      }
      if (!restarted) return current;
    }
    const err = new Error('ELOOP: too many symbolic links encountered, realpath ' + JSON.stringify(String(p)));
    err.code = 'ELOOP';
    throw err;
  }

  function encodeAs(options, result) {
    const encoding = typeof options === 'string' ? options : options && options.encoding;
    return encoding === 'buffer' ? Buffer.from(result) : result;
  }

  if (typeof module === 'object' && module.exports) {
    module.exports.walkRealpathSync = walkRealpathSync;
  }

  if (typeof fs.realpathSync.native === 'function') {
    const nativeSync = fs.realpathSync.native;
    jsRealpathSync.native = function (p, options) {
      try {
        return nativeSync.call(this, p, options);
      } catch (e) {
        if (!isPermissionError(e)) throw e;
        try { return jsRealpathSync(p, options); } catch (e2) { /* walk below */ }
        try { return encodeAs(options, walkRealpathSync(p)); } catch (e3) { throw e; }
      }
    };
  }

  if (typeof fs.realpath.native === 'function') {
    const nativeCb = fs.realpath.native;
    jsRealpath.native = function (p, options, cb) {
      if (typeof options === 'function') { cb = options; options = undefined; }
      if (typeof cb !== 'function') return nativeCb.call(this, p, options, cb);
      return nativeCb.call(this, p, options, (err, resolved) => {
        if (!isPermissionError(err)) return cb(err, resolved);
        jsRealpath(p, options, (err2, resolved2) => (err2 ? cb(err) : cb(null, resolved2)));
      });
    };
  }

  const nativePromise = fsp.realpath;
  fsp.realpath = async function (p, options) {
    try {
      return await nativePromise.call(this, p, options);
    } catch (e) {
      if (!isPermissionError(e)) throw e;
      return new Promise((resolve, reject) => {
        jsRealpath(p, options, (err2, resolved) => (err2 ? reject(e) : resolve(resolved)));
      });
    }
  };

  // Reads of a hidden rc file report ENOENT. See isHiddenRcFile.
  const readFileSyncOrig = fs.readFileSync;
  fs.readFileSync = function (p, ...rest) {
    try {
      return readFileSyncOrig.call(this, p, ...rest);
    } catch (e) {
      if (isPermissionError(e) && isHiddenRcFile(p)) throw enoentFor(p);
      throw e;
    }
  };

  const readFileOrig = fs.readFile;
  fs.readFile = function (p, ...rest) {
    const cb = typeof rest[rest.length - 1] === 'function' ? rest.pop() : null;
    if (!cb) return readFileOrig.call(this, p, ...rest);
    return readFileOrig.call(this, p, ...rest, (err, data) => {
      if (isPermissionError(err) && isHiddenRcFile(p)) return cb(enoentFor(p));
      cb(err, data);
    });
  };

  const readFilePromiseOrig = fsp.readFile;
  fsp.readFile = async function (p, ...rest) {
    try {
      return await readFilePromiseOrig.call(this, p, ...rest);
    } catch (e) {
      if (isPermissionError(e) && isHiddenRcFile(p)) throw enoentFor(p);
      throw e;
    }
  };

  // yarn asks fs.exists before it reads ~/.npmrc, and the sandbox answers true
  // for a file it then refuses to open (measured 2026-10-06: stat, access and
  // exists succeed on the real home's .npmrc and .yarnrc, the read gives EPERM).
  // A file reported present and then missing would fail yarn the same way, so
  // the existence check agrees with the read: a hidden rc file that cannot be
  // opened does not exist. One that can be opened still does.
  function isReadRefused(p) {
    try {
      fs.closeSync(fs.openSync(p, 'r'));
      return false;
    } catch (e) {
      return isPermissionError(e);
    }
  }

  const existsSyncOrig = fs.existsSync;
  fs.existsSync = function (p) {
    const found = existsSyncOrig.call(this, p);
    return found && isHiddenRcFile(p) && isReadRefused(p) ? false : found;
  };

  const existsOrig = fs.exists;
  if (typeof existsOrig === 'function') {
    const exists = function (p, cb) {
      if (typeof cb !== 'function') return existsOrig.call(this, p, cb);
      return existsOrig.call(this, p, (found) => {
        cb(found && isHiddenRcFile(p) && isReadRefused(p) ? false : found);
      });
    };
    // util.promisify(fs.exists) resolves with the boolean through this hook.
    if (existsOrig[require('util').promisify.custom]) {
      exists[require('util').promisify.custom] = (p) => new Promise((resolve) => exists(p, resolve));
    }
    fs.exists = exists;
  }
} catch (e) {
  // Never the reason a contained process fails.
}
