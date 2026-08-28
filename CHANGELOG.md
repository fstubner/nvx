# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

* **`isolation.filesystem.allow_read_exec`: let a contained tool reach a program
  kept outside the project.** The sandbox grants the project, a throwaway home
  and nvx's own runtimes, and nothing else — so a tool whose executables live
  elsewhere cannot run them.

  Playwright is the case that forced it. Its browsers are in
  `%LOCALAPPDATA%\ms-playwright`, and a contained process could not list that
  directory at all: measured EPERM inside against 27 entries outside, and
  `LIST_OK` inside once the root was granted.

  ```json
  { "isolation": { "filesystem": {
      "allow_read_exec": ["%LOCALAPPDATA%/ms-playwright"] } } }
  ```

  Read and execute only, never write. Paths expand `~`, `$VAR` and `%VAR%` so one
  file works across machines; a path that is not present here is skipped with a
  warning rather than failing the run. Adding one counts as loosening, so a
  checked-in project file needs the same approval an egress allowlist entry does.
  On Windows the grant goes to the project's own capability rather than the shared
  package identity — these ACEs persist on disk, so granting the shared identity
  would have admitted every sandbox on the machine rather than only this project's.

  **The grant is recorded, and withdrawn when the policy stops asking for it.** It
  persists between runs by design -- re-applying it every launch would put a
  permissions call on the startup path for every root -- so nvx keeps a ledger of
  what it granted and reconciles it on each contained run in that project. Delete
  the entry, or the whole policy file, and the next run takes the permission off
  disk. `nvx grants list` shows them; `nvx grants reset` withdraws them, instead of
  deleting its own records and orphaning the permissions they tracked, which is
  what it did before. A record it cannot read is kept rather than deleted, and
  reported, since removing it would strand exactly what it was there to track.

  This also corrects the MCP containment design, which recorded that
  browser-driving servers cannot be contained on Windows because connections
  *into* an AppContainer are refused. True when reaching a browser already running
  on the host — but when Playwright launches its own, the browser is a child
  inside the container and is reached over intra-container loopback, which nvx
  already proves works. Ports were never the blocker for that case; the binary
  being unreachable was.

  **Linux grants reading and executing, not listing.** A contained process can
  read and run files under a granted root but cannot `readdir` it. That is not
  specific to this feature: `/usr/bin` and `/etc` cannot be listed either, and
  they have carried the directory-read right since the Linux sandbox was written.
  Recorded in README rather than worked around.

* **`--connect` (Windows): let a contained tool reach one service already running
  on your machine.** The mirror of `--expose`, and the missing direction. The
  sandbox has no route to your loopback — Windows refuses an AppContainer's
  loopback connections, and the egress proxy declines host loopback destinations
  on purpose — so a contained tool that must talk to a browser with remote
  debugging on, a local database or a device emulator had no way to get there.

  ```
  nvx --connect 9222:19222 npx some-tool --endpoint http://127.0.0.1:19222
  ```

  nvx runs a listener inside the sandbox and dials `127.0.0.1:9222` itself from
  outside. The contained side chooses when to connect, never where. Both ends
  close when the command exits.

  The two numbers must differ, for the same measured reason `--expose` requires
  it: an AppContainer shares the host's network stack rather than getting its own,
  so one port cannot hold both the in-sandbox listener and the real service. Omit
  the second (`--connect 9222`) and nvx picks a free port, prints it, and sets
  `NVX_CONNECT_9222` in the sandbox — useful for a tool that reads its endpoint
  from the environment, not for a command line your shell expands before nvx runs.

  In a policy file it is `isolation.network.connect_ports`; adding one counts as
  loosening, so a project cannot grant itself a host port without approval. This
  is deliberately not the machine-wide loopback exemption removed in 0.5.0, which
  opened every service on 127.0.0.1 to every sandbox permanently and needed
  elevation to revoke — `--connect` grants nothing at the OS level at all.

  On macOS and Linux the flag parses and warns that it does nothing, rather than
  being silently ignored. (`--expose` now warns there too; it was silent before.)
  Both warn when the command is not sandboxed at all, for the same reason.

### Fixed (found by an independent acceptance pass before release)

* **A `--connect` grant is now confined to the sandbox that asked for it.** It was
  not. Windows permits loopback within an AppContainer package and every nvx
  sandbox shares one package identity, so the in-sandbox listener was reachable
  from every other nvx sandbox running at the same time: measured, a sandbox in an
  unrelated project with no grant of its own read the granted service. nvx now
  identifies the process behind each tunnel connection, refuses any that is not
  part of this run, and logs the refusal. Unverifiable peers are refused rather
  than admitted. This was a known hazard in the wrong place -- the egress relay has
  the same exposure and has defended itself with a per-session credential since an
  acceptance pass found the equivalent attack on 2026-08-19.

* **A directory granted by `allow_read_exec` no longer becomes permanently
  unwritable if it is later used as a working directory.** The check for an
  existing grant matched on identity alone, never on which rights were held, so a
  read-only entry convinced the modify grant it had nothing to do. Every write in
  that directory then failed with EPERM, and nothing in the product could clear it
  -- repeat runs, `grants reset --all`, `doctor --fix` and deleting the policy
  entry all left it broken. Removing the entry by hand made it worse: the grant
  cache still reported success, so no grant was re-applied and contained commands
  could not even enter the directory. Rights are now part of both the check and
  the cache key.

* **An explicit `--connect <host>:<inside>` is no longer overridden by a policy
  entry for the same port.** The policy won the de-duplication silently, handing
  back a different in-sandbox port, so a command line naming the port it asked for
  pointed at nothing.

* **A self-referential `%VAR%` in a policy path no longer hangs nvx.** Expansion
  restarted from the beginning of the string after each substitution, so a
  variable containing its own name looped without bound and nothing launched --
  no error, no output.

* **A lost grant record no longer strands a permission for ever.** Only what a
  run had just written was recorded, and an entry from an earlier run was
  indistinguishable from someone else's, so once a record was lost — a corrupt
  file, a deleted grants directory, one failed save — every later run re-confirmed
  the permission and declined to write it down again. nvx now recognises its own
  entry by its exact signature, so the record is re-created and the permission can
  still be withdrawn.

* **`nvx grants reset` no longer deletes a record it has just reported it cannot
  act on.** An unreadable record was treated as one holding nothing, so it was
  removed along with the rest — destroying the only trace of permissions still on
  disk, and reporting success. It is kept and counted now. A second unreadable
  record also no longer overwrites the first.

* **Withdrawing a grant now clears the cache for the whole directory tree.** The
  entries nvx writes are inheritable, so withdrawing one on a parent removes the
  access its children had through it; the cache still listed those children as
  granted, so their grant was skipped and the sandbox got EPERM on a directory the
  policy still named — for up to seven days, while the log said it had been
  granted.

* **Withdrawing a read/execute grant no longer removes a permission nvx did not
  grant.** Withdrawing is not selective -- it removes an identity's whole entry on
  a directory, not one right from it. Two things made that destructive: a grant
  skipped because a broader entry already covered it was still recorded as though
  nvx had written it, and reclamation ran after the writable roots were
  established rather than before. Naming the project's own directory in
  `allow_read_exec` and then removing it therefore deleted the write access the
  project needed, and the next run died with "chdir: Access is denied". Only what
  nvx writes is recorded now, and reclamation runs first so anything still needed
  is re-established after it.

* **A permission check no longer reads the directory's own path as permissions.**
  It matched on the whole line of `icacls` output, which begins with the path, so
  a directory whose name contains `(M)`, `(RX)` or `(R)` was read as already
  holding those rights -- the grant was then skipped and the wrong answer cached,
  leaving the sandbox unable to reach a directory the policy named.

* **A grant record that cannot be read is no longer discarded.** It was parsed
  leniently and then overwritten, which stranded every permission it listed:
  invisible to reclamation and to `nvx grants reset`, removable only by hand. It
  is now kept under a `.unreadable` name and reported. Records are also written
  atomically, so an interrupted write cannot produce that state in the first
  place.

* **`TestReadExecRootsAreNeverWritable` now tests what it claims.** It asserted
  that an unrelated directory was absent from a set built from two other paths --
  true regardless of what the code under test did, and it passed with the
  enforcement path deliberately given write permissions. It now checks the access
  mask the rule is built from. Its comment also claimed the Windows side was
  covered by an enforcement probe; no probe covers this feature, and the claim is
  gone rather than reworded.

## [0.5.7] - 2026-08-28

### Fixed (found by an independent acceptance pass before release)

* **`npm run <script>` is no longer sandboxed when the script's name happens to
  match a package-manager verb.** A project with a script called `update`,
  `create`, `rebuild`, `dedupe`, `upgrade` or `add` had `npm run <that>` silently
  contained — scrubbed environment, restricted egress, confined writes — around a
  command that is the developer's own code. Measured: `npm run build` ran
  directly, `npm run update` ran "in native sandbox".

  Widening the verb set in 0.5.6 turned a long-standing assumption into a live
  bug: both token scans read every non-flag argument, so a script's *name* was
  read as a subcommand. The scans now stop at `run`/`run-script`, after which
  everything belongs to the script — the same rule already applied to `--`.

  The justification in the code was that mistaking a token for a verb "pushes a
  command toward MORE containment, never less — the safe direction". This project
  rejected exactly that reasoning for `--strict` one release earlier. More
  containment is not free when it lands on a command that was never untrusted,
  and on Windows a blocked write outside the project can report success while
  producing nothing.

* **`nvx --help` and README documented the pre-0.5.6 `--strict` rule**, telling
  users it works "before the command or among its arguments". It has not since
  0.5.6, so anyone following the help text believed their own code was contained
  when it was not. README also contradicted itself on the same page.

* The comment in `shim_options.go` describing which smuggled flags are honoured
  has now been wrong in both directions at different times, and says so.

* The hand-run Windows release gate could pass having asserted nothing: it exited
  0 when Node was missing, and its runtime-install check read only the exit code
  of the *second* of two commands. Both are failures now — the same
  warn-instead-of-fail shape CI's Linux step was changed to reject.

* **Dead classification code for runtimes this build does not manage is gone.**
  `classifyInvocation` still had branches for `uv` and `deno`, and `uvx`/`pyx`
  sat in the ad-hoc-tool list — left behind when the Deno, Go and Python
  providers were removed. None of those names is in any provider's shim list, so
  nvx never saw those commands and the code could not run. Unreachable code that
  reads as support for a runtime is worse than none; the real implementations are
  preserved on `feature/polyglot-runtimes`.

* **CI now prints the probe counts instead of the docs quoting them.**
  `docs/enforcement-matrix.md` cited "442 pass and 35 skip" from a hosted Windows
  run someone had read by hand. It had already rotted once, and the correction
  could not be checked by a reviewer at all — a developer machine *runs* the
  probes a hosted runner skips, so the number was unreproducible by design. Every
  run now emits `NVX_PROBE_COUNTS pass=… skip=… fail=…` to its job summary and
  log, along with the distinct skip reasons, and the page says where to look
  rather than carrying a figure that goes quietly wrong.

* nvx stopping a command 15–30 seconds after its launcher exits, with exit 129,
  is now documented in README rather than only in this file — including the case
  it can catch by surprise: a deliberately detached process that still has a pipe
  on its input.

### Fixed

* **Sandboxed MCP servers no longer pile up after their client exits.** Measured
  on the maintainer's machine: 18 nvx processes, 12 of them orphaned, 43 node
  processes holding 3.9 GB between them, the system freezing. Every orphan was
  `nvx shim npx <an MCP server>`.

  nvx only left when two signals agreed: its input pipe had hung up *and* the
  process that started it had exited. On Windows a sibling that inherited the
  write end of that pipe keeps it open indefinitely, so the first signal never
  arrived. nvx had already recorded the reason itself, in its own hangup log:
  "the parent has exited, but something still holds the input pipe open".

  The parent having exited is now enough on its own. That is safe because of when
  the watchdog arms at all: only when stdin is a pipe, meaning someone
  deliberately wired a channel to nvx. If the process that did that is gone,
  nothing is talking to it.

  It does not reopen the regression the two-signal rule was added for. That was a
  finished shell pipeline, where the producer closes its end and the pipe reads as
  broken while the shell that built the pipeline is still waiting — the parent is
  alive there, so nothing fires. It was the pipe half that was the wrong signal.
  Both cases now have a test, and the orphan one was confirmed to fail against the
  old rule before the fix and pass after.

  Deliberate detachment is unaffected: `start /b` leaves stdin a console, where
  the watchdog never arms.

## [0.5.6] - 2026-08-24

### Security

* **Commands that fetch and run untrusted package code were not being contained.**
  `npx` was; the identical operation spelled any other way was not:

  | Command | Before | Now |
  |---|---|---|
  | `npm exec`, `pnpm dlx`, `yarn dlx`, `bun x` | not contained | contained |
  | `npm create`, `npm init <initializer>` | not contained | contained |
  | `npm update`, `npm rebuild`, `npm dedupe`, `npm audit fix` | not contained | contained |
  | `yarn upgrade`, `pnpm update`, `bun update` | not contained | contained |

  These ran with no sandbox, no vulnerability scan and no typosquat check.
  `npm exec cowsay` fetched a package from the registry and executed it
  uncontained, while `npx cowsay` — the same thing — was contained. `npm rebuild`
  re-runs every dependency's install scripts. `npm audit fix` is the command you
  run *because of* a security advisory. `npm create vite` is how a project starts.

  README claimed the opposite in four places and SECURITY.md in one, and no
  limitations section mentioned it. No test covered it in either direction, which
  is why it survived: the classification tests listed `npx`, `bunx`, `uvx`, `pyx`
  and stopped. Found by an independent acceptance review of the 0.5.5 build,
  which is why 0.5.5 was never published.

  The reverse is now tested too — `npm run build`, `npm test`, a bare `npm init`
  and `npm audit` without `fix` still run uncontained, because a security tool
  that contains everything is one people switch off.

### Fixed

* **`--strict` is no longer read from a command's own arguments.** It must lead,
  like `--no-sandbox` and `--standard`.

  It was honoured anywhere on the reasoning that it only ever *adds* containment,
  so smuggling it gained nothing. True of an attacker, wrong for everyone else:
  `--strict` is TypeScript's most-used flag and ESLint's. `nvx tsc --strict` meant
  "typecheck strictly" and nvx read it as "sandbox this", moving the command into
  a container where writes outside the project are redirected to a throwaway home
  — and on Windows such a write reports success, so a build could appear to work
  and produce nothing.

  Same defect as 0.5.5's fix for nvx *removing* those flags, in the opposite
  direction: both came from treating a word that belongs to other tools as nvx's
  own. nvx still notices it and now says why it did nothing.

* **A skipped privileged containment test fails CI instead of warning.** The step
  printed "it is verifying nothing" and let the job go green — the exact condition
  that let three Linux checks report success for months.

* Documentation corrected where it was less careful than the code:
  `SECURITY.md` said "shimmed commands run inside an OS-native sandbox" without
  the your-own-code distinction README makes; the `NVX_PROBE` skip count in
  `CONTRIBUTING.md` counted top-level skips while `go test -v` prints one more for
  a subtest.

## [0.5.5] - cut, never published

Tagged and built, then blocked by an independent acceptance review that found
the containment gap listed under 0.5.6 above. Nothing was released under this
version; everything below ships in 0.5.6. Kept as its own section because the
work is separable and the reason it did not ship is worth being able to find.

### Fixed (found by an independent acceptance pass before release)

* **nvx no longer takes your program's arguments away from it.** It read its own
  flags — `--no-sandbox`, `--strict`, `--standard`, `--filesystem-provider` — out
  of a wrapped command's arguments and *removed* them, anywhere in the line, past
  `--`, silently:

  ```
  nvx npx tsc --strict            → tsc ran WITHOUT --strict
  nvx npx electron --no-sandbox   → electron never saw it
  nvx node app.js -- --strict     → stripped past the end-of-options separator
  nvx node app.js --filesystem-provider notes.txt keep  → "notes.txt" eaten too
  ```

  Those names are not nvx's to take: `--strict` belongs to TypeScript and ESLint,
  `--no-sandbox` to Chromium and everything embedding it. A non-strict typecheck
  was being reported as a strict one, with no error. It applied to uncontained
  runs too, where nvx has no security interest at all.

  nvx now *notices* these flags without confiscating them, and stops reading at
  `--`. The anti-bypass rule is unchanged and still tested: a weakening flag
  smuggled through a package manager's arguments is still refused — it is just
  passed on to the program as well, and you are told it was ignored.
  `--filesystem-provider` now only reads the documented `=` spelling, because
  finding the value of the separated form meant consuming an argument that
  belonged to the program.

* **The loopback-exemption warning is now tested on a healthy machine.** It is the
  only mitigation for a hole this project's own documentation calls serious, and
  the single test covering it skipped unless the machine already carried an
  exemption — so it skipped in CI and everywhere else, while the enforcement
  matrix claimed the check was "pinned by" it. The detection now has a seam, and
  four tests cover the exempt branch, the healthy branch, and the `nvx doctor`
  line that had no test at all.

* **The Windows containment gate can no longer pass by mistaking a regression for
  an environment limit.** It skipped on *any* launch failure, so a change that
  broke every launch would have read as "this host cannot create AppContainer
  children". It now skips only on the two refusal shapes a hosted runner actually
  produces, and fails on anything else.

* **A release can no longer be built from a commit whose CI has not passed.** The
  release workflow tested only on Ubuntu and did not wait for the cross-platform
  run — the v0.5.5 draft was complete while that commit's CI was still going. It
  now waits, using `gh` rather than a third-party action, because this is the job
  that publishes the binaries of a supply-chain security tool.

### Added

* **`--expose`: a server running inside the Windows sandbox can now be reached
  from the host.** A contained `npx vite` used to bind its port, print that it
  was listening, and serve nobody — Windows refuses connections into an
  AppContainer, and no setting changed that.

  ```
  nvx --expose 5173:8080 npx vite
  ```

  Also settable per project as `isolation.network.expose_ports: ["5173:8080"]`.
  Omit the second number and nvx picks a free port and prints the URL.

  **The two numbers cannot be the same.** An AppContainer shares the host's
  network stack rather than getting its own, so one port cannot hold both the
  contained server and the host listener; with both set to 51733 the contained
  server lost the race and died with `EADDRINUSE`. Give the port your server uses
  inside, then the port you want to visit.

  **Nothing is relaxed to make this work.** The contained side dials *outward*
  over a UNIX socket and the parent splices inbound requests onto those tunnels,
  so no network capability is granted and egress stays exactly as restricted. The
  end-to-end test asserts both in the same run: the host reaches the contained
  server, and the contained process still cannot reach the internet. Adding a
  port to a project policy counts as loosening it, so it needs the same approval
  an allowlist entry does — publishing puts whatever the sandbox serves onto
  loopback, where a browser extends it the trust localhost carries.

### Changed

* **Windows containment can now be re-checked by running one script, rather than
  resting on having been measured once.** `scripts/sandbox-enforcement-windows.ps1`
  joins the Linux and macOS probes and asserts the same five outcomes: writes and
  reads outside the project denied, both allowed inside, egress denied with an
  empty allowlist. Verified on Windows 11, including the control that matters —
  run with containment off, the same probe reports the opposite and the forbidden
  file appears on disk, so it distinguishes a working sandbox from a broken one.

  Hosted Windows runners refuse to create AppContainer children, so this cannot
  be gated in CI the way the other two are. It is a documented pre-release step
  in `CONTRIBUTING.md`, together with the `NVX_PROBE=1` suite — the twenty-odd
  end-to-end checks that skip on hosted CI and cover a sandbox reading another
  project, a deny ACE hiding a secret, and a session reading another's guest
  home. Last measured 2026-08-23: 319 pass, 0 fail, 3 expected skips, each named
  so a fourth is a signal.

  The existing Windows smoke test had the flaw the Linux enforcement script was
  written to fix: it checked a host write by writing through the sandbox's
  redirected `%USERPROFILE%`, which is meant to succeed, so it would have passed
  against a sandbox restricting nothing. It had no read assertion at all.

* **Contained commands on Windows got faster after a project's first run.**
  Measured on Windows 11, a warm `nvx --strict shim node -e 0`: **~650ms before,
  ~390ms after**. Bare `node -e 0` on the same machine measures ~210ms, so most
  of what remains is Node starting rather than nvx.

  Nothing about the sandbox changed. nvx was re-reading every access-control
  entry on every launch: **17** `icacls` processes per command, each measured at
  ~20ms. Sandbox setup measured ~410ms in total, of which the permission phase
  alone measured ~250ms. The permission work itself is trivial — the cost was
  starting a process to ask. About ten of those paths give the same answer on
  every run for a given project and runtime, so nvx now remembers which grants it
  has verified and stops re-asking.

  Only *positive* answers are remembered, which is what makes it safe: a stale
  "already granted" makes a launch fail, and can never make the sandbox more
  permissive. Entries expire after a week, and any failed launch clears them all,
  so a permission removed behind nvx's back is repaired on the next run rather
  than needing anyone to know a cache file exists.

* **macOS now proves three things it previously only described.** An allowlisted
  host must be *reachable* through the proxy, not merely a blocked one refused —
  every earlier assertion ran with an empty allowlist, so all of them would have
  passed against a sandbox that had failed to start. UDP is checked separately
  from TCP, and turns out to be refused harder than expected: at bind rather than
  at send. And nvx refusing to run at all when `sandbox-exec` is missing is now a
  test rather than a reading of the code.

  One macOS claim is still not made: the outbound connection the probe watches
  being refused could be failing at DNS or at connect, and nothing distinguishes
  them. On macOS that difference is real, so it stays open.

### Fixed

* **A hung AppContainer launch no longer takes the whole Windows test suite with
  it.** One probe's launch stopped returning on a hosted runner, so `go test` hit
  its package timeout and reported failure for everything — a runner's shape
  presented as a product defect. It is now bounded, and reports "hung" distinctly
  from "refused" so a real regression cannot hide behind an environment excuse.

* **Two CI checks reported the wrong reason for skipping.** The Windows egress
  smoke stopped on the runner's Node version before reaching anything about nvx;
  CI now installs a version that has the flag it needs, so it skips on the real
  blocker instead. Nothing about the product changed — only whether the log tells
  you the truth about what was and was not checked.

## [0.5.4] - 2026-08-23

### Fixed

* **The Linux sandbox could not run anything.** Every contained launch died with
  `Sandbox execution failed: fork/exec <runtime>/bin/node: no such file or
  directory`, naming a file that was present, executable, and inside the rules
  the sandbox had just granted.

  The target was being started in a nested user namespace of its own, and asking
  for one means the parent writes the uid and gid mappings through
  `/proc/<child>/`. By that point the supervisor has applied its Landlock ruleset,
  which grants nothing under `/proc`, so the kernel refused the write and the
  process never started.

  It reported a missing file rather than a refusal because the supervisor runs in
  its own PID namespace with the host's `/proc` still mounted: the child's process
  id names nothing there, so the path fails to open before permissions are
  considered. The same launch outside that namespace says "permission denied".

  The nested namespace is gone. The target keeps its own mount namespace, and
  takes the user namespace from the supervisor, which is where it belongs --
  membership in that one is what allowed the mount namespace unprivileged in the
  first place. Nothing is less contained; a second namespace only gave the target
  a fresh one to be root in.

* **Three Linux checks in CI reported success without testing anything**, which
  is why the above survived. Each gated itself on `unshare -n`, which an ordinary
  user is refused — nvx asks for a network namespace and a user namespace
  together, which is exactly what makes it available unprivileged. So all three
  skipped on every unprivileged machine, including the runner they were written
  for.

  The containment probe additionally explained its own silence: finding no report
  from the contained process, it blamed the distribution for restricting user
  namespaces and exited zero, without checking whether they were restricted. They
  were not. It now tests that before deciding, and fails if the sandbox had every
  facility it needed and still produced nothing.

  With them running, both smoke tests turned out to be launching their probe
  uncontained, so the one that checks a sandboxed process cannot write outside
  its project could never have passed and the other was measuring the host's
  internet connection. Both now run strictly contained.

### Changed

* **The published guarantees now match the evidence, in both directions.** README,
  `SECURITY.md`, `PRODUCT.md` and `docs/enforcement-matrix.md` said macOS was
  unverified at runtime and that Linux rested on privileged CI. Neither is true
  now: each runs an enforcement probe on a hosted runner of its own OS, asserting
  what must be denied *and* what must still be allowed, so a sandbox that refuses
  everything fails rather than passes.

  Four macOS cells stay unclaimed rather than rounded up — that an allowlisted host
  completes through the proxy, that UDP in particular is refused, that nvx fails
  closed without `sandbox-exec`, and which layer refuses the outbound connection
  the probe does observe being refused.

  macOS still does not contain reads. That is now asserted rather than merely
  admitted: the probe requires the read to succeed, so tightening the profile fails
  CI and forces all four documents to move in the same change.

## [0.5.3] - 2026-08-23

### Added

* **`nvx audit` — a local record of what nvx did, for reviewing later rather than
  in the moment.** `~/.nvx/audit.log` already held security decisions (egress
  allow/deny, grants, policy pins) but nothing recorded that a command ran at
  all, so questions that only show up across many runs had no evidence behind
  them: how often something falls out of the sandbox and why, which warnings fire
  repeatedly and get scrolled past, what is slow.

  **Per-run records are off by default.** Set `NVX_TRACE=1` to turn them on for a
  session. Recording every command a developer runs is a debugging aid, not
  something to switch on for people who did not ask for it, however local the
  file stays. Security decisions remain unconditional — those are a record of
  what nvx refused, which is the point of running it.

  With it on, each top-level invocation appends one record: command, subcommand,
  whether it was contained, why not when it was not, exit code, duration, and the
  warnings it printed. Nested invocations are not recorded — one typed `npm`
  command can spawn a tree of lifecycle scripts, and counting each would make
  every total wrong.

  `nvx audit` prints them interleaved with the security decisions they caused;
  `--summary` gives counts, `--failures` narrows to non-zero exits.

  **Nothing is sent anywhere.** The file is on local disk and there is no
  uploader.

  **Arguments are not recorded.** A subcommand is, but only when the word is one
  nvx recognises — `install`, `run`, `publish` — and only when it can be reached
  without stepping over a flag that might take a value. Everything else is
  dropped rather than guessed at, because argv is where the private things live:
  `npm config set //registry/:_authToken=…`, `node -e '<script>'`, `node
  acme-client-secret.js`, and `yarn deploy-acme-prod`, where yarn's bare
  positional is your own script name. This is the file a user would paste into a
  bug report. Warnings are recorded as their
  message template, never as the rendered text, so a warning that quotes a
  package URL stores `…checks for %s.` and not the credentials in it.

  The live log is capped at 4 MB and one rotated generation is kept, so expect up
  to about 8 MB on disk. It grew without limit before, which was tolerable when
  only security events went in it.

* **A remembered permission failure no longer costs a slow command every week.**
  nvx skips ancestor-directory grants that have failed before, because retrying
  them costs three seconds and buys nothing. Those records expired after seven
  days and all expired at once, so the first contained command after the deadline
  paid the entire three-second retry budget — against roughly 0.6s for a normal
  contained run.

  Records now last a month, and at most one is re-tested per run. An environment
  that starts working is still noticed within a few commands; the cost of
  checking is about a second a month instead of three seconds a week.

* **A contained command can stream its child's output.** `spawn(cmd, {stdio:
  'pipe'})` inside the sandbox used to block forever before the child existed —
  Windows builds piped stdio out of named pipes and a contained process cannot
  create one. Every contained `npx vitest` or `npx playwright` run left a process
  wedged until someone killed it; 17 were found on one machine, some blocked for
  13 hours.

  nvx now creates the pipes outside the container and the preload only opens
  them, which Windows permits when the pipe names both the user and that
  container's identity. Output streams as it is produced, stdout and stderr stay
  separate, exit codes propagate, and 20,000 lines arrive complete and in order.

  One gap: **writing to a contained child's stdin is not supported** —
  `child.stdin` is `null` rather than a stream that silently discards.

  Beyond 8 concurrent piped children in one process, output is collected and
  delivered when the stream ends rather than as it is produced. No bytes are
  dropped, but they arrive on `close`, not before `exit` — so a caller that reads
  its accumulated buffer in an `exit` handler sees it full for the first 8
  children and empty for the rest. nvx warns once when a process crosses that
  line. Windows only.

* **Abandoned sandbox profiles are reclaimed automatically.** They used to wait
  for someone to run `nvx cleanup`, and nobody did — 91 had accumulated on the
  development machine. Each run now reclaims a few after the command finishes,
  skipping any whose owning process is still alive, so a concurrent install
  cannot lose its home. Bounded per run, so one command never pays for a whole
  backlog.

  A process killed outright cannot clean up after itself, so leftovers are
  unavoidable; needing a command to deal with them was not. `nvx cleanup` still
  reclaims everything at once, and still prunes staged supervisors from other
  builds — that part stays manual, because a supervisor another nvx has staged
  but not yet run cannot be told apart from an unused one.

### Fixed

* **nvx processes piled up forever once the program that started them exited.**
  Measured on a development machine: 48 live `nvx` processes, 38 of them
  orphaned, each still holding a sandbox supervisor, accumulating at about one
  per 60–80 seconds until Windows ran out of commit charge and builds started
  failing with "the paging file is too small". Nearly all were MCP servers
  started through `nvx shim npx …`.

  nvx waits for the command it launched. An MCP server does not stop when its
  stdin reaches end-of-file — many do not — so when the client went away, the
  server kept running, nvx kept waiting, and nothing ever ended either. The
  process-tree cleanup that already existed was never the problem: it fires when
  nvx exits, and nvx never exited.

  nvx now stops when two things are true together: the pipe it was given for
  input has lost its writer, **and** the program that started it has exited.
  Stopping triggers the existing cleanup and takes the sandbox with it.

  Both conditions are required because either alone is wrong. A producer closing
  its end is how a pipeline is meant to finish — `echo hi | nvx node …` must not
  be killed — and a parent exiting is normal for deliberately detached commands.
  Only together do they mean nobody is left to talk to. If the parent cannot be
  identified, nvx leaves the command alone.

  Windows only; on Linux and macOS the ordinary signal path already ends an
  abandoned process.

* **The README's command list was missing five commands**, some for several
  releases: `doctor`, `grants` (both `list` and `reset`), `import`, `setup` and
  `shim`. A reader checking whether nvx could inspect its grants would have
  concluded it could not. `nvx help` had its own gap in the other direction — it
  never listed `version`.

  Both lists are now complete, and a test compares them so the next omission
  fails instead of shipping. Nothing breaks when documentation is wrong, nobody
  re-reads a list they wrote, and the person who notices is the one who already
  gave up on the feature.

  The README's flag section was also misleading: it listed `--no-sandbox` under
  "shim flags", which reads as `npm --no-sandbox install`. In that position the
  flag is stripped and ignored — deliberately, so nothing escapes the sandbox by
  appending a flag to npm. It only works as `nvx --no-sandbox npm ...`, and the
  README now says so. `--strict` and `--standard` were not documented there at
  all.

## [0.5.2] - 2026-08-20

### Security

* **"Builds coexist" was not true, and the prune that made it untrue could race a
  launch.** Staged supervisor copies were pruned on every launch, keeping only the
  copy the running process wanted -- so alternating a release and a dev build
  re-copied ten megabytes each time, and a second nvx could delete the first's
  copy in the window between staging it and executing it. That is a narrower
  version of the race per-build naming exists to remove.

  Launches no longer prune other builds; they only clear the legacy fixed-name
  copy and leftover temporary files. Reclaiming disk moved to `nvx cleanup`, where
  nothing is mid-launch. Verified by alternating two builds and watching both
  copies survive, then reclaiming them with `cleanup`.

* **The async-pipe limitation is documented for tool runners, not only installs.**
  `npx`/`bunx` tools that stream a child's output hang exactly as an install does,
  which README and SECURITY.md now say. The two-minute hint still covers installs
  only, deliberately: an install running that long is anomalous, an `npx`-launched
  dev server running for hours is working, and a timer cannot tell them apart. A
  hint that fired on the second would be noise, and a hint people learn to ignore
  is worse than none.

* **A typo in `isolation.network.mode` silently gave you more network than you
  asked for.** An unrecognised value fell through to proxy in every reader, so a
  policy saying `"offlin"` got a live egress proxy while its author believed they
  had asked for no network at all. Nothing warned. The neighbouring
  `isolation.level` already reported an unrecognised value, and this is the field
  where being wrong grants more access rather than less.

  Unrecognised modes now warn once, name the valid values, and are rewritten to
  proxy so no downstream reader falls into its own default arm -- which is also
  where the platforms diverged: the same typo produced proxy on Windows and Linux
  and no network rule at all on macOS. Warned rather than refused, matching
  `isolation.level`, and because proxy is the restrictive default: you asked for
  stricter than the default and get the default, loudly.

* **A corrupted staged supervisor bricked every contained launch, permanently.**
  The staged copy's name encodes the source binary's size and timestamp and the
  reuse check compares size, so a corruption preserving size -- an antivirus
  quarantine stub, a cloud-sync placeholder, a bad sector -- was invisible to it.
  Every later launch failed identically, and neither `cleanup` nor `doctor` could
  clear it. Found by an acceptance pass zeroing 512 bytes in place; it is the
  failure the previous commit set out to eliminate, reached by another route.

  An image error from `CreateProcess` now discards the staged copy and retries
  once. The trigger is narrow -- five image-specific Windows codes, matched by
  code rather than message text so it works on a localised Windows -- because a
  launch refused for permissions must not spin. The launch error is wrapped with
  `%w` for the same reason; it was `%v`, which stringified the code away.

* **A project whose path contained "(I)" hid its own pre-0.5.0 grant.** icacls
  prints a directory's path on the same line as its first entry, and the scan
  skipped any line containing "(I)" in order to skip inherited entries. For such a
  path the skip swallowed that first entry instead, so a dangerous `(OI)(CI)(M)`
  grant was invisible to both `doctor` and the launch-path cleanup: the project
  stayed writable by every sandbox on the machine while `doctor` called the
  install healthy and `--fix` did nothing.

  Inheritance is now read from the rights the SID carries rather than from the
  line, so the path cannot influence it. Same class of mistake as the one this
  function was corrected for one commit earlier -- matching on the shape of text
  instead of on its meaning. Pinned by a test using directories named `proj(I)x`
  and `(I)`, confirmed to fail against the previous filter, with a companion test
  that inherited entries are still skipped.

* **README claimed contained dev servers work; on Windows they do not.** The FAQ
  said loopback bind/connect works for dev servers, with no platform qualifier. A
  server started inside the sandbox binds and reports itself listening, but
  Windows refuses connections into an AppContainer from outside it -- the same
  restriction the egress relay exists to work around, and unaffected by the
  loopback exemption. `nvx npx vite`, `npx serve` and anything else serving a port
  appear to start and then serve nobody. Corrected in the FAQ and added to Known
  limitations, with the workaround: `npm run dev` is uncontained at the default
  level anyway, and `nvx --no-sandbox npx <tool>` covers the tool-runner case.

* **The stale-grant check reported live grants as leftovers, and `--fix` deleted
  them.** The scan matched any AppContainer package SID on a directory, ignoring
  what the entry actually granted. But the current design writes `(X,RA)` --
  traverse and read-attributes, non-inheritable -- on the directories above a
  sandbox so it can walk through them without listing them. Run nvx from a
  subdirectory and the project root carries one, so on a healthy machine `doctor`
  announced it as a pre-0.5.0 leftover letting "any nvx sandbox read and write
  this project", exited non-zero, and offered to remove it. False in every clause
  for a traverse-only entry, and the removal took out a grant nvx had just
  written. The regex also matched entries belonging to unrelated AppContainer
  applications.

  The scan now matches on what an entry grants, not on the SID being present.
  Anything beyond the traverse pair is stale, a strict subset of it is not, and
  unreadable rights stay quiet -- this drives both a security claim shown to the
  user and a removal, so asserting either from an entry that could not be parsed
  is what caused the false positive. Legacy `(OI)(CI)(M)` grants still match.
  Introduced in the previous commit and caught by the next acceptance pass.

* **`nvx doctor` now finds the pre-0.5.0 grants that leave a project writable by
  every sandbox on the machine.** Up to 0.5.0 every sandbox ran as one shared
  package identity and the grants it wrote were never revoked, so a contained
  install in one project can read and write another. The cleanup only ever ran on
  the working directory of the session currently running, so a project you do not
  revisit stays exposed indefinitely.

  README has disclosed this since 0.5.0, and the containment behaviour is
  unchanged -- what was missing was any way to observe it. The comparable
  leftover, the loopback exemption, warns on every contained launch and fails
  `doctor`; this one was silent. An acceptance pass found 19 such grants live on
  the nvx repository itself while `doctor` reported a healthy install and exited
  0, and wrote into that repository from a sandbox scoped to a different project.

  `doctor` now reports leftover grants on the project it runs in, counts them
  against health, and removes them under `--fix`. nvx keeps no record of where it
  has run, so it cannot sweep the machine; the check answers for the directory you
  are standing in. It is not a launch-path warning because the launch path already
  removes them from the working directory before running anything.

  `docs/enforcement-matrix.md` said an unqualified "Yes" for Windows write
  containment, contradicting README's own Known limitations. Corrected.


* **On a platform with no sandbox, nvx ran the command unprotected and said it
  had contained it.** `sandbox_native_other.go` -- the path for any Unix that is
  not Linux or macOS -- set a process group, logged "using environment isolation
  only" at info level, and then executed the command. nvx printed "Running in
  native sandbox" around it. A contained install on FreeBSD therefore had full
  access to the user's home, their SSH keys and the network.

  This is exactly what SECURITY.md's design stance rules out: if a containment
  primitive is unavailable, refuse to run rather than run unprotected. Every other
  platform honoured it. It now refuses, naming `--no-sandbox` as the deliberate
  opt-out. A scrubbed environment and a redirected HOME are worth having but are
  conventions the child can ignore, so they do not amount to a sandbox and are no
  longer presented as one.

  No release binaries are published for these platforms, so this was reachable
  only by building from source -- which is exactly the person who would trust the
  word "sandbox" without checking. Closes F28, and with it F33, the last open
  contradiction of the fail-closed claim. Pinned by
  `TestUnsupportedPlatformRefusesInsteadOfRunningUnprotected`, which asserts the
  command left nothing behind rather than trusting its exit code.

* **On macOS, a contained process could reach every service on your loopback
  address.** Every restricted mode emitted `(allow network-outbound (remote tcp
  "localhost:*"))`, so an install could reach a local database, a daemon's TCP
  port or another project's dev server with no `allow_hosts` entry. Worse than it
  first sounds: any reachable service that forwards traffic -- a debugging proxy,
  `ssh -D`, a dev server's proxy route -- turns that into unrestricted egress, so
  the allowlist stopped meaning anything. `network.mode: offline` was covered by
  the same rule, so offline was not offline. Present since the sandbox was first
  implemented.

  Loopback is now granted per mode. `proxy` reaches the proxy's own ports and
  nothing else; `offline` emits no network rule at all; `loopback` keeps the
  wildcard, that being the entire meaning of the mode. A `proxy` launch with no
  known proxy port now grants nothing instead of falling back to the wildcard.
  The per-port rules that already existed were dead code -- the wildcard above
  them had subsumed them since the beginning.

  Pinned by `TestSeatbeltGrantsLoopbackOnlyWhereTheModeMeansIt`, confirmed to fail
  against the previous behaviour. This fixes the generated Seatbelt profile; per
  the enforcement matrix, no macOS hardware has yet confirmed the kernel enforces
  any part of that profile.

### Fixed

* **Shims pointed at whichever binary generated them, so a source build broke
  them.** `generateShims` embedded `os.Executable()`. Run `init-shims` from a
  build tree -- which the smoke scripts do -- and every shim depended on a file
  that gets rebuilt, moved or deleted, after which `npm` failed with a
  missing-file error naming a path in someone's source directory. It happened
  three times in one session on the machine this was found on, and left that
  machine with no working `nvx` on PATH at all, which is why the `-g` refusal's
  advice could not be followed. Shims now point at `<nvxHome>/bin/nvx`, where the
  installer puts nvx, and a copy is installed there if missing. Verified by
  running the smoke script and deleting the repo build: the shims kept working.

* **`yarn global add` was not recognised as a global install.** Only `-g` and
  `--global` were matched, and yarn spells it as a subcommand -- so it fell
  through to the sandbox and failed partway with the confusing permission error
  that check exists to replace. `yarn global add/remove/upgrade` are now caught,
  by position rather than by containment, so `yarn add global` still installs a
  package called "global".

* **A failed contained install pointed at a debug log that had already been
  deleted.** Reported from real use. npm writes its log into the cache, which nvx
  redirects into the guest home, and the guest home is removed when the run ends
  -- so the path npm printed was dead before the user finished reading it, on
  exactly the occasion the log is wanted. The logs are now copied to
  `~/.nvx/logs/<session>` first, and only when the command failed; a successful
  run leaves nothing behind, not even an empty directory. The guest home stays
  ephemeral.

* **The loopback-exemption warning was four lines on every contained command.**
  Every clause was true and worth saying once, but in a real transcript it was
  most of the output of an ordinary `npm` invocation, and a warning that dominates
  every command is one people stop reading. It is now a single line pointing at
  `nvx doctor`, which still reports the full explanation and the exact removal
  command and still exits non-zero. Still shown on every launch rather than once a
  day: it is a live weakening of the containment being asked for.

* **`@latest` silently skipped two security checks, and scanned the wrong
  thing.** Reported from real use: `npm install -g npm@latest` produced a
  vulnerability alert listing seven advisories with no descriptions. The noisy
  output was the symptom. The cause was that version resolution replaced the
  query only when it was *empty*, so the literal string `"latest"` was carried
  forward -- and every lookup keyed on it missed:

  - `meta.Versions["latest"]` misses, so **the install-script prompt never
    appeared** for any `@latest` install;
  - `meta.Time["latest"]` misses, so **the release-age check never fired**;
  - OSV was queried for version `"latest"`, so the scan was not about the version
    being installed.

  Every dist-tag now resolves -- `latest`, `next`, `beta`, whatever a publisher
  defines -- and an exact version wins over a same-named tag. Verified against the
  live registry: `npm@latest` now scans clean, and `esbuild@latest` correctly
  warns about install scripts naming the resolved `esbuild@0.28.2`, which it
  previously did not warn about at all.

  **Behaviour change worth knowing:** a semver range (`lodash@^4.17.0`) is now an
  explicit "could not verify registry metadata ... proceed?" rather than a silent
  pass. nvx cannot check a version it cannot name, and quietly running no checks
  is what this replaces. Resolving ranges properly would remove the prompt and is
  not done here.

* **Advisories printed as bare identifiers with no description.** OSV's
  `querybatch` returns ids and modification times only, so every finding rendered
  as `GHSA-xxxx-xxxx-xxxx: ` with nothing after the colon -- unactionable, and it
  reads like the tool is broken. Summaries are now fetched per advisory,
  best-effort and bounded: a failed lookup leaves the line exactly as it was
  before, so this can only improve the message and never blocks an install on a
  second network call.

* **A refused global install still ran a vulnerability scan and asked you to
  approve it first.** Reported from real use. `npm install -g npm@latest` printed
  a scan, listed advisories, asked "Proceed with installation despite active
  vulnerabilities?", took the yes -- and then refused, because `-g` cannot run
  contained. The refusal depends only on the flags, so it was knowable before any
  of it. The delay is the smaller half: approving a security prompt for a command
  that could never run trains people to click through the prompt that matters.
  The check now runs before verification.

* **The refusal told you to run a command your shell could not find.** It suggests
  `nvx --no-sandbox ...`, which needs `nvx` on PATH. The installer puts it in
  `~/.nvx/bin` beside the shims, but a source build that runs `init-shims` leaves
  shims pointing into the build tree with no `nvx` installed -- and then the advice
  produces "The term 'nvx' is not recognized", which reads as the user's mistake.
  The hint now falls back to the running binary's absolute path when `nvx` does not
  resolve.

  Two related hazards were found and are recorded, not fixed: shims embed the
  absolute path of whichever binary generated them, so running `init-shims` from a
  build tree makes every shim depend on a file that may be rebuilt, moved or
  deleted; and `yarn global add` is not recognised as a global install at all
  (`globalInstallFlags` is `-g`/`--global`), so it is contained and fails with the
  confusing permission error this check exists to prevent. Pinned by
  `TestYarnGlobalSubcommandIsNotYetRecognised` so it stays visible.


* **A hung contained process could block every later contained launch.** The
  sandbox supervisor was staged under one fixed name, and Windows refuses to
  replace a running executable -- so a supervisor still alive from an earlier
  build held that file and the next launch died at staging with a bare "Access is
  denied", naming a path inside `~/.nvx` that nobody would connect to their stuck
  process. The only cure was finding and killing it by hand. Asynchronous piped
  stdio still hangs, so leaving a supervisor alive is not a rare accident.

  Staged copies are now named per build, so nothing is ever replaced: builds
  coexist and a leftover copy is inert rather than blocking. Copies from other
  builds are pruned when they are not in use, failures ignored, because a copy
  that will not delete is one another sandbox is executing. The legacy fixed-name
  copy is cleared on the next launch.

  The name is derived from the binary's size and timestamp rather than a content
  hash: this is a launch path measured in milliseconds and hashing ten megabytes
  to learn what a stat already tells us would be a poor trade.

* **The macOS network mode was the only reader of that field not trimming
  whitespace.** A policy carrying `"mode": "proxy "` was `proxy` on Windows and
  Linux and matched no case in the Seatbelt switch, so macOS emitted no network
  rule at all. It failed closed, but it was a silent, platform-divergent
  behaviour change from one trailing space in a config file.

### Changed

* **The docs stop claiming macOS enforcement nobody has tested.** Three tables
  printed the same "Yes" for Windows, Linux and macOS on guarantees backed by very
  different evidence -- measured, CI-tested, and never checked respectively. macOS
  cells now read "profile only" and say what that means. The one macOS check in CI
  asserts that a sandboxed process can write its own working directory and nothing
  about what is blocked, so it would pass against a build whose sandbox blocked
  nothing.

* **Two documents contradicted each other on a security question.** SECURITY.md
  said macOS egress control was cooperative and a raw socket could bypass the
  allowlist; README said macOS blocks raw TCP/UDP at the OS. README was right --
  the profile is `(deny default)` -- so SECURITY.md understated the design while
  the matrix overstated the evidence for it. Both corrected.

## [0.5.1] - 2026-08-19

### Fixed

* **`npm install esbuild` hung forever inside the sandbox, and now does not.**
  A contained process may not create a named pipe, and Windows implements piped
  child stdio with named pipes, so a postinstall that captures its own
  subprocess blocked inside libuv before the child existed. 0.5.0 shipped this
  as an unfixable limitation. That was half right.

  The restriction really is unliftable, and that is now measured rather than
  assumed: `CreateNamedPipeW` inside a real AppContainer returns
  `ERROR_ACCESS_DENIED` for every name shape tried, so it is the NPFS device
  refusing and no name routes around it. Granting it would mean loosening
  `\Device\NamedPipe` for every AppContainer on the machine, UWP apps included.

  But file descriptors were never restricted, and synchronous capture does not
  need a stream -- its whole contract is "run it, give me the output at the
  end". A preload in every contained node process now routes `spawnSync`,
  `execSync` and `execFileSync` through temp files in the guest home. esbuild
  installs in seconds and the resulting binary bundles correctly. The preload
  falls back to the original function on any error, because it loads into every
  contained node process and must never be the reason one fails.

  Async `spawn(..., {stdio:"pipe"})` is a real stream a file cannot stand in
  for and still hangs; it stays under Known limitations with the two-minute
  hint. Containment is untouched -- this changes how a contained process talks
  to its own children, not what it can reach.

* **An AppContainer's temp directory never existed.** Windows redirects a
  sandboxed process's temp to `<LOCALAPPDATA>\Packages\<pkg>\AC\Temp`, ignoring
  the `TEMP` nvx sets. Nothing created it, so `os.tmpdir()` inside the sandbox
  pointed at a missing directory and every scratch file failed with ENOENT --
  which is most tools. Found while diagnosing the above.

* **The lifecycle smoke fixture could not fail on the bug it was written for.**
  Its postinstall only wrote a file; it never captured a subprocess, so it
  passed while `npm install esbuild` hung. It now captures a child and asserts
  the captured text. Verified by disabling the preload and watching the smoke
  hang.


## [0.5.0] - 2026-08-18

Everything below shipped in the `v0.5.0` tag. Entries that sat under
[Unreleased] while the tag was cut have been folded in: for a security tool
whose changelog is the disclosure channel, a reader of the 0.5.0 section must
see what the build they downloaded actually contains.


### Security

* **One sandbox could borrow another's egress allowlist.** Every nvx sandbox on
  a machine shares one AppContainer package identity, and Windows scopes its
  loopback restriction to the package -- so two projects running at once sat in
  the same loopback namespace. An acceptance pass port-scanned loopback from
  project B, found project A's relay, and tunnelled to a host only A's policy
  allowed, in a run where B's own proxy refused that host. The host-side proxy
  listeners had the same exposure to any local process.

  Each session now mints a random credential and its proxy requires it, over
  HTTP and SOCKS alike. It travels as ordinary proxy credentials in HTTP_PROXY,
  so npm, node and curl send it without knowing anything about nvx, while a
  sibling that found the port by scanning gets 407. Authentication is checked
  before the allowlist, so the 403-vs-200 difference cannot be used to probe
  what another session may reach. Replaying the original attack now yields 407
  from both the relay and the host proxy.
* **A loopback exemption left by an older `nvx setup` opened every service on
  127.0.0.1 to contained code, and nothing said so.** The whole Windows egress
  design rests on Windows refusing an AppContainer's loopback connections. Before
  0.5.0 the proxy ran on the host's loopback, so an elevated `nvx setup`
  registered an exemption to reach it -- which also opened local databases,
  daemon ports and other dev servers. 0.5.0 never registers one and removes it
  during `nvx setup`, but that command needs an Administrator terminal and this
  release tells users it is no longer required, so on an upgraded machine the
  exemption simply stayed: the allowlist looked enforced, other hosts really were
  blocked, and loopback was wide open.

  nvx cannot remove it unelevated, so it now detects it and warns on every
  affected contained launch -- every launch, not once, because it is a live
  weakening of what the command was asked to do. `nvx doctor` reports it and
  exits non-zero. Both print the exact removal command. A clear result is cached
  for a day so a healthy machine pays nothing per launch; the exempt result is
  never cached, so the warning stops the moment the exemption is gone.

  Worth recording why this survived: `TestLoopbackIsNotAutomaticallyAllowed`
  exercises the proxy's allow decision, which cannot see an OS-level exemption,
  so no test failed while the guarantee did. The new check is pinned against the
  machine's real exemption list instead.

* **A contained install could plant a command that ran later, uncontained, as
  you.** `nvx use` puts the project-bin shim directory near the front of PATH,
  ahead of System32. That directory used to live inside the project, so a
  postinstall could write a file called `git` into it and wait; the next `git`
  the developer typed ran it with their full user token -- every credential
  store, every project, unrestricted network. No sandbox bug was involved. The
  containment held and was routed around by a directory nvx itself put on PATH.
  Reproduced end to end in PowerShell and Git Bash.

  Two changes, and it needs both. The directory moved under `~/.nvx`, which the
  sandbox cannot write. And generation now refuses to shim a name that already
  resolves elsewhere on PATH, because `node_modules/.bin` is itself writable by
  an install -- so relocating alone would just move the plant one directory
  back. Stale wrappers are pruned on every regeneration.

  The cost, stated plainly: if you have a global tool of the same name, the
  project-local one no longer wins through nvx. `npx <tool>` still runs the
  local one, contained.

* **`--agent-mode` let a repository switch containment off.** The flag exists
  for AI agents, and it sets the same blanket yes as `-y`/`NVX_YES` -- which
  covered the two prompts that decide the security model itself: trusting a
  project's own `.nvx-policy.json` when it loosens settings, and adding a host
  to the egress allowlist. So an agent cloning a repository it had not read
  would auto-approve that repository's request to disable the sandbox, and the
  approval persisted. Measured: a policy carrying
  `{"isolation":{"enabled":false}}` was refused without the flag and silently
  trusted with it; arbitrary egress hosts, including an IP on a C2-style port,
  were approved and written to the grants store.

  Those two prompts no longer accept a blanket yes. They need an interactive
  answer, or `NVX_TRUST_YES` -- deliberately not `NVX_YES`, because nothing
  sets it by habit. Ordinary prompts (vulnerability warnings, install-script
  confirmations) still honour `-y`, so non-interactive installs do not stall.

* **Permissions left by pre-0.5.0 nvx were only partly cleaned.** The cleanup
  removed one identity, on the project you happened to be standing in --
  measured at 19 stale entries on a single directory, each still granting
  modify access to every sandbox. It now removes every AppContainer package
  identity from the directories it grants. Projects nvx never revisits still
  keep theirs, which README.md and SECURITY.md now say plainly along with the
  manual command.

* **Git Bash got no protection at all on Windows, and `nvx doctor` said it was
  fine.** The shim directory held only `npm.cmd`/`npm.ps1`, which bash never
  selects: it looks for a file named exactly `npm` and does not consult PATHEXT.
  A bare `npm install` in Git Bash therefore ran the real npm -- unaudited,
  unsandboxed -- while `doctor` reported interception as healthy, because it was
  answering the PATHEXT question rather than the one bash asks. Agent harnesses
  on Windows commonly run Git Bash, which is the case nvx exists for.

  Extensionless shims are now written alongside the others, and `doctor` reports
  their absence and no longer calls that state healthy.

* **Security prompts hung instead of failing closed when stdin was not a
  terminal.** `PromptYesNo` opened the console directly and treated "a console
  exists" as "a human is present". On Windows `NUL` is itself a character device,
  so `< /dev/null` -- what CI steps and agent harnesses do -- looked interactive,
  and an install stopped at a prompt nobody could answer. README and SECURITY.md
  both promise the operation is denied in that case. Interactivity is now decided
  by `GetConsoleMode` on Windows and a terminal ioctl on Unix. Measured: an
  install that previously hung past 90s now denies and exits in 2s.

### Fixed

* **`npm install esbuild` hung forever inside the sandbox, and the docs said it
  could not.** SECURITY.md and README claimed npm installs were unaffected by
  the named-pipe restriction because lifecycle scripts inherit stdio. That fixes
  npm's own piping and nothing else: a postinstall that captures its OWN
  subprocess still blocks, because the restriction is on the contained process
  creating the pipe. esbuild's postinstall does exactly that. Measured at no
  completion after 13 minutes contained, against 8 seconds uncontained.

  This is an OS restriction nvx cannot lift, so it is now stated as a known
  limitation instead of denied, with the workaround (`--no-sandbox` for that
  package). A contained install still running after two minutes prints a hint
  naming this cause, because the failure mode was silence -- and silence reads
  as "nvx is broken" rather than "this package needs a flag". The smoke test
  that was supposed to catch this could not: its fixture's postinstall only
  writes a file, never capturing a subprocess. That is now recorded next to the
  claim it failed to check.

* **`nvx policy init` still scaffolded a fourth dead key.**
  `isolation.filesystem.mode` was missed when the other three inert keys were
  dropped, so every new policy shipped a security-looking key whose value read
  "strict" and which no decision consulted. It is gone, along with the empty
  `"prompts": {}` that pointed readers back at the removed keys, and the dead
  defaulting and merging code behind them.

* **Upgraded machines let the sandbox list `~/.nvx`.** Homes created before
  0.5.0 carry a read+execute grant where fresh ones get traverse-only, and the
  "is it already granted" check answered yes to either, so it was never
  narrowed. nvx now narrows it once per home.

* **Three wording corrections where the text overstated the guarantee.** The
  loopback-exemption warning said egress to other hosts was unaffected -- an
  acceptance pass disproved it by completing a TLS exchange with an external
  host through a CONNECT proxy on loopback, so any forwarding service on
  127.0.0.1 makes egress arbitrary. `--help` still advertised `nvx setup` as
  adding allowlisted egress, which 0.5.0 removed. And the install-script prompt
  asked whether to run scripts "on your host" when they run contained.

* **The removal command was hidden by `-q` and `--agent-mode`,** leaving the
  loopback warning visible and its fix invisible. It is a warning now, not an
  info line.
* **`nvx use` silently did nothing in Git Bash on Windows, and reported
  success.** Shell detection always answered PowerShell there, so nvx emitted
  assignments bash cannot evaluate: nothing applied, `node -v` was unchanged,
  and it still printed "Now using Node.js". Auto-switch on `cd` never fired
  either. Git Bash and MSYS2 are now detected.

* **`--agent-mode` is documented as `-y -q` and only ever did the `-y` half.**
  It now sets quiet too. That gates success and info lines only; warnings and
  errors still print.

* **`nvx setup --undo` could not remove the profile-root grant** that README
  and SECURITY.md said it removed. It now sweeps that path as well.

* **Four documented policy keys did nothing.** `prompts.interactive`,
  `prompts.non_interactive`, `prompts.network_unknown` and
  `isolation.filesystem.mode` were parsed, merged, scaffolded by
  `nvx policy init` -- and read nowhere, so tightening one was silently
  ineffective. They are no longer written into new policies and README says
  they are unimplemented. Existing policies still parse.

* **A stray `ACC_WRITE_PROBE.tmp` shipped inside the v0.5.0 tag** -- a
  reviewer's escape probe swept in by `git add -A`. Removed, and `*.tmp` is
  now ignored.

* **A contained command took seconds to start.** The sandbox grants traverse
  rights on the directories above your project. On some chains -- `AppData` and
  below, where a filter driver stalls the ACL write -- that grant never
  completes, and nvx retried it on every single launch, burning its whole 3s
  budget each time for something that could not succeed.

  Measured before changing anything: the grants are not needed. With the
  ancestor walk skipped entirely a contained process still launches, stats and
  writes its working directory, and most of those directories are reachable
  anyway from permissions Windows already sets. Failed grants are now remembered
  and not retried for a week; one that succeeds clears the record. Same command,
  same machine: **5.3s to ~1.05s**.

  Measure it on your own machine before relying on that figure. An independent
  review measured **~2.2s** steady state on the same fix, against 0.4s
  uncontained, and the first contained run after a new runtime is staged costs
  **45s to 3 minutes** while the runtime is copied. The 1.05s above was one
  measurement on a warm home with a managed runtime already in place, published
  as though it were the number; it is the best case, not the typical one.

* **`nvx node --strict app.js` ran uncontained.** `--strict` was stripped from
  the wrapped command's arguments and then discarded, so you got neither the
  containment you asked for nor an error saying you had not got it -- and
  `nvx help` shows the flag without saying it must come first. It is now
  honoured wherever it appears. `--standard` deliberately still is not: it
  reduces containment, so a dependency's own arguments must not be able to
  weaken the sandbox around it.

* **A contained process could list the names inside directories nvx granted for
  traverse.** The ancestor grant used `(RX)`, which includes list-folder. It is
  now traverse and read-attributes only, which is what it was always described
  as being. Your home directory itself remains listable from any sandbox -- that
  is an ACE Windows ships, not one nvx adds -- and is now stated in
  `docs/enforcement-matrix.md` rather than implied away.

* **Installing any package with a lifecycle script hung forever on Windows.**
  A process inside an AppContainer is not permitted to create a named pipe, and
  Windows builds piped child stdio out of named pipes -- so npm's default of
  piping lifecycle-script output blocked inside libuv before the child process
  existed. The target's own timeout never fired, because its event loop never
  got another turn. This affected exactly the class of package the sandbox
  exists to contain, and `npm install` of anything with a `postinstall` never
  returned.

  Lifecycle scripts now inherit stdio instead, which npm supports directly.
  Their output goes to the terminal as it happens rather than being buffered,
  which for a tool whose job is to show you what a package does during install
  is not a regression.

  Found by an independent acceptance pass, not by the test suite: every existing
  test launched contained children *from the parent*, and none had a contained
  process spawn one. `scripts/sandbox-smoke.ps1` now installs a dependency
  carrying a postinstall and fails if it does not finish, and that check has been
  confirmed to fail against the previous behaviour.

  The underlying restriction remains: anything else inside the sandbox that
  captures a child's output still hangs. That is recorded in README.md under
  Known limitations and pinned by a probe, so the workaround can be removed if
  the restriction ever lifts.


### Security (continued)

* **A sandboxed command could reach every project nvx had previously run in, and
  read credentials a trusted tool had persisted.** The AppContainer profile is
  stable by design, so every session ran as the same identity, and the filesystem
  permissions nvx grants were never revoked. A permission added while installing
  in project A was therefore still valid when nvx later ran in project B.
  Measured: a contained install read *and wrote* a second project's files, read a
  concurrent session's guest home, and read a `tool_home` profile's credential.
  This contradicted README.md, which claimed other projects were unreachable.

  Writable roots are now granted to a capability derived from the project rather
  than to the shared identity, so a session elsewhere does not hold it. Stale
  permissions from earlier versions are removed the first time nvx runs in an
  affected project. Sessions in the same project still share one identity, which
  is deliberate.

* **The new egress relay had opened a route to services on your own machine.**
  The proxy permitted every loopback destination unconditionally, which was
  harmless while nothing contained could reach the proxy at all. Relaying to a
  proxy that runs outside the sandbox changed that: a contained process could
  reach any local service -- a database, a dev server, another agent -- with an
  empty allowlist. Loopback is now allowlisted like any other destination.
  `network.mode: loopback` still permits it; `offline` no longer does.

### Fixed (continued)

* **`nvx doctor` rewrote your persistent PATH without being asked.** It repaired
  a shadowed PATH the moment it found one, and because the repair targets
  whichever `NVX_HOME` is set, pointing that at a throwaway directory silently
  fronted the real user PATH with it. Diagnosis is now read-only; the repair
  moved behind `nvx doctor --fix`, which the report points at. A flag rather
  than a prompt deliberately: `NVX_YES` is set as a matter of course by agents
  and CI, so a prompt would auto-approve a persistent system change for exactly
  the callers least able to notice it.

* **An interrupted install blocked that version from ever installing again.**
  The lock file recorded a pid that nothing read back, so Ctrl-C during a
  download left a lock no command cleared -- `nvx cleanup` does not touch
  install locks -- while the error said "already in progress", sending you after
  a process that no longer exists. A lock whose owner is provably gone is now
  cleared and reported. One whose owner is alive, or whose contents cannot be
  parsed, is still respected.

* **`nvx cleanup` deleted guest homes belonging to sandboxes that were still
  running**, so running it during a concurrent install destroyed that install's
  `HOME` mid-run. Sessions now record their owning process and are skipped while
  it is alive.


**0.4.0 was tagged but never published.** The tag was cut, then held back
because the README it shipped with made three claims about the sandbox that
were subsequently disproved by execution — including that a malicious package
could not read your `.env`, which it can. Those claims were corrected before
this release. So 0.5.0 is the first published build carrying the 0.4.0 fixes
as well as its own, and the newest downloadable build before it remains
`v0.2.0-beta`.

### Security

* **Windows egress is now actually restricted to the allowlist.** It was not
  before, on any released build. The sandbox held the `internetClient`
  capability, so `HTTP_PROXY` was a request a package could simply decline —
  measured against 0.4.0, a `postinstall` script opened connections to
  `1.1.1.1:443` and `registry.npmjs.org:443` with no restriction at all.

  The AppContainer is now granted no network capability, so Windows itself
  refuses direct connections and DNS does not resolve. The parent's egress proxy
  is exposed on an AF_UNIX socket — a filesystem object, which the AppContainer
  network restriction does not cover — and a new in-container supervisor,
  `nvx __appcontainer-exec`, re-exposes it as loopback TCP for tools that only
  understand `host:port`. That is the same parent-proxy-plus-relay shape Linux
  already used, and it needs no elevation.

  The same `postinstall` script now gets `EACCES` and `ENOTFOUND` for both hosts
  while `npm install` completes normally against the real registry.

* **`nvx setup` no longer registers a loopback exemption, and removes an existing
  one.** The exemption was how a sandbox reached the proxy before the relay; it
  also let the sandbox reach every other loopback listener on the machine. With
  the relay it grants access for no remaining reason. Setup is now only about
  drive-root stat access, and is no longer needed for allowlisted egress.

### Fixed

* **Every sandboxed command failed on Windows without an nvx-managed runtime.**
  A runtime nvx does not manage is copied somewhere the sandbox can reach, and
  that copy walked the source with `filepath.Walk` — which inspects each path
  with `Lstat`, so a directory *link* arrived looking like a file and the copy
  tried to open its own destination folder for writing. nvm for Windows makes
  `C:\Program Files\nodejs` exactly such a link, and it is how most Windows
  developers install node, so the whole sandbox died with
  `open <nvxHome>\sandbox-exec\<hash>: is a directory` — a message naming
  neither node nor the link.

  Staging now recurses with `os.ReadDir`/`os.Stat`, which follow both kinds of
  Windows directory link. Resolving the path up front would have fixed only
  half: a symbolic link sets `ModeSymlink` and `filepath.EvalSymlinks` resolves
  it, but a junction reports `ModeIrregular` and `EvalSymlinks` returns it
  unchanged with no error. Links below the root are followed too; previously
  they would have been copied as empty folders, leaving a runtime missing files
  and failing later somewhere unrelated.

* **The same path then launched node against a directory it could not read.**
  The staged copy was used for the interpreter while `npm-cli.js` still pointed
  at the original directory, so node failed with `Cannot find module
  C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js`. The command is now
  made reachable before it is rewritten, so both come from the copy.

### Changed

* `network.mode: open` is now the only mode that grants the Windows sandbox a
  network capability. An unrecognised mode relays rather than connecting direct,
  so a typo cannot silently disable the allowlist.
* Proxied Windows runs fail closed if the egress socket cannot be created, rather
  than falling back to a direct connection.

## [0.4.0] - 2026-08-18

**0.3.0 was never published.** It has a dated entry below and `version.go` claims
it, but no `v0.3.0` tag exists, so the newest downloadable build is `v0.2.0-beta`
— which predates every fix listed here. Anyone tracking releases has none of this.

### Security

Each of these was reproduced by execution, not inferred from reading.

* **Linux containment did not work at all.** Every sandboxed launch failed on
  `/dev/null`: the read-only rules requested a directory-only Landlock right on a
  character device, which the kernel rejects, and the failure was fatal. It failed
  closed, so nothing was exposed — the feature was simply dead on every Linux system.
* **Linux proxy mode could not reach allowed hosts.** The egress proxy was started
  *inside* the loopback-only network namespace, leaving allowlisted traffic no route
  out. The proxy now runs outside the namespace and the contained process reaches it
  over a UNIX socket, which a namespace does not contain.
* **The Linux seccomp filter was inverted**, allowing the UDP it claimed to block
  and denying the AF_UNIX the sandbox needs. Unreachable while containment was dead;
  fixed alongside it.
* **`linux/arm64` used the wrong syscall numbers** — every entry one too high, so
  `landlock_restrict_self` invoked `memfd_secret` and the error misleadingly blamed
  the kernel version. This is a published release target.
* **The Linux sandbox could read all of `~/.nvx`**, including other tools' persisted
  credentials in `tool_home`, the grants pin store, and `policy.json`. Narrowed to
  the runtime trees it actually needs.
* **macOS granted the sandbox write access to `~/.nvx` and the runtime directory**
  on the default path, so a contained process could rewrite `policy.json`,
  self-approve grants, poison `npm_global`, or replace the `node` binary every later
  run executes. A persistent sandbox defeat.
* **Windows never delivered piped stdio to the sandboxed child**, so every
  stdio-protocol daemon — that is, every MCP server — failed deterministically, while
  interactive use looked healthy.
* **Orphaned sandbox processes were not cleaned up** (Linux reaping, Windows job
  object), so abandoned launches accumulated until the tool had to be removed.
* **A cached binary path is now validated before execution** instead of being
  trusted to still be on `PATH`.
* **Fixed a data race in the egress proxy** that could abort a run outright: the
  session map was read without the lock guarding its writes, and ordinary parallel
  package-manager traffic could trigger it.

### Changed

* **macOS: `npm install -g` inside the sandbox is now denied**, matching Windows and
  Linux. Global installs write under `~/.nvx`, which is no longer writable from
  inside. This is the documented design, but it is a behaviour change for anyone who
  relied on the gap.
* **Windows: `nvx setup` is optional.** The sandbox runs unelevated; setup adds the
  loopback exemption that enables allowlisted egress.
* **Windows sandbox startup is roughly 13x faster** — a measured launch went from
  90.7s to 6.6s. The cost was an ACL walk re-granting directory ancestors on every
  launch, hanging on one of them until it timed out.
* **Uncontained runs are announced** rather than proceeding silently.

### Fixed

* **Documentation corrected where it overclaimed.** `README.md`, `SECURITY.md` and
  `docs/enforcement-matrix.md` stated that Windows restricts egress to the policy
  allowlist. It does not, unless an elevated `nvx setup` has run: by default the
  sandbox is granted `internetClient` and the proxy variables are stripped, so the
  allowlist is never consulted — not even cooperatively.
* **CI verifies containment instead of reporting green.** Both Windows smoke tests
  exited 0 unconditionally on CI, and the egress test asserted only that blocked
  traffic fails — which a sandbox denying everything passes perfectly. The privileged
  Linux tests now run, and both egress tests assert the allow path too.

### Added

* `nvx doctor` — diagnoses and repairs shim interception, including a shadowed
  persistent `PATH` on Windows.
* `nvx grants list` and `nvx grants reset [--all]`.
* `nvx import`, quiet/agent-mode flags, and wildcard trusted-package patterns.
* Containment v2: subcommand-aware classification, `isolation.level`
  (`standard`/`strict`), and `--strict`/`--standard` flags.
* Persistent per-tool guest profiles under `~/.nvx/tool_home`, so a trusted tool
  keeps its own state without being handed the real home directory.

### Known limitations

* **Windows egress is not allowlisted without an elevated `nvx setup`.** A
  no-elevation design has been shown feasible — an AppContainer can reach an AF_UNIX
  socket held by the parent, and intra-container loopback works — but it needs an
  in-container supervisor that does not exist yet.
* **The macOS fixes are verified at profile-generation level only.** Whether
  `sandbox-exec` enforces the generated profile as written has not been re-tested on
  macOS hardware.
* **The sandbox re-entrancy marker is a plain environment variable** and can be
  forged by a process able to set its own environment.

## [0.3.0] - 2026-07-05

### Added
* **Bun runtime**: `nvx install bun@1.2` (and `bun`/`latest`), managed the same way as Node.js with mandatory checksum verification. `bun`/`bunx` shims route to the Bun provider.
* **`runtime@version` CLI**: install/use/default/uninstall accept a runtime prefix; a bare version stays Node.js for nvm compatibility. Node and Bun can be active in one shell without evicting each other from `PATH`.
* **FilesystemProvider registry**: `native` and `docker` are first-class; `wsl`/`wslc`/`systemd-nspawn` are gated behind `NVX_EXPERIMENTAL=1`. An unavailable backend (e.g. Docker not running) fails closed before launch.
* **Docker hardening**: image selected per runtime; `offline`/`loopback` enforced with `--network none`; `--cap-drop=ALL`, `no-new-privileges`, `--pids-limit`, `tmpfs /tmp`.
* **Audit log**: `~/.nvx/audit.log` records egress allow/deny and policy-trust events.
* **Docs**: `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `docs/runtime-providers.md`, `docs/enforcement-matrix.md`, and a tag-triggered release workflow.

### Changed
* **JS runtime focus**: shipped runtimes are Node.js and Bun only. Deno, Go, Python, and uv/pyx work remains on the `feature/polyglot-runtimes` branch.
* **Project policy trust**: approved egress hosts persist under `~/.nvx/grants` (outside the project tree) instead of `.nvx-policy.json`. A project policy file that would weaken settings is ignored unless its exact contents are trusted for that project (prompted once; fail-closed when non-interactive).
* **Fail-closed policy parsing**: the Linux sandbox child aborts on a policy parse error instead of falling back to defaults.
* **Faster shims**: resolved runtime binary paths are cached (keyed by `PATH`) so the shim skips the expensive Windows `PATH` scan on repeat calls — dispatch overhead drops from ~100 ms to ~38 ms on Windows, and measures ~3 ms on Linux and ~4 ms on macOS (GitHub-hosted runners). See `scripts/bench.py`.

## [0.2.0-beta] - 2026-07-02

### Added
* **Isolation v1 policy schema**: `isolation.filesystem.provider` and `isolation.network.mode` replace the flat `isolation.provider`; top-level `runtime` and `prompts` blocks.
* **Shim-only sandbox path**: `npm`, `node`, `npx`, `yarn`, `pnpm`, and `bunx` run sandboxed by default when `isolation.enabled` is true; use `--no-sandbox` to bypass per invocation.
* **Embedded egress proxy**: `network.mode: proxy` starts an in-process HTTP CONNECT + SOCKS5 proxy on loopback with policy allowlist and interactive approval for unknown hosts (persisted to `.nvx-policy.json` on approve).
* **RuntimeProvider execution hooks**: binary resolution and default network allowlists go through `RuntimeProvider` so sandbox code is not Node-specific.
* **Cross-platform smoke tests**: filesystem, egress block, and macOS runtime smokes in CI.
* **`nvx policy init`**: scaffold global and project policy files.
* **Project bin shims**: sandbox `node_modules/.bin` tools via `.nvx/project-bin/`.

### Changed
* **Default isolation**: `isolation.enabled` defaults to `true`; `network.mode` defaults to `proxy`.
* **Removed legacy CLI**: `nvx sandbox`, `nvx s`, `nvx exec`, and the `nvxs` shim target are removed; shims are the sole sandbox entry point.
* **Fail-closed Windows native path**: AppContainer setup failure no longer falls back to Low IL alone.
* **Linux network isolation**: loopback-only network namespace with in-child egress proxy; seccomp blocks UDP and offline TCP.

### Removed
* **`--provider` flag**: use `--filesystem-provider=` on shim invocations instead.

## [0.1.0] - 2026-06-30

### Added
* **Multi-Platform Swapping**: Zero-dependency swapping of Node.js versions in under a millisecond by modifying only session-level shell environment variables (`PATH`, `NPM_CONFIG_PREFIX`), supporting PowerShell, Zsh, and Bash.
* **Auto-Configuration Swapping**: Instantly switches Node.js version when navigating into directories containing configuration files (`.nvmrc`, `.node-version`, `package.json`, or Volta configurations).
* **Dynamic PATH Shim Architecture**: Uses dynamic shims in `~/.nvx/bin` to intercept execution reliably in subshells, IDEs, and scripts, resolving early shell alias vulnerabilities.
* **Registry Checksum Integrity**: Enforces cryptographic integrity for Node.js downloads using SHA-256 hashes from nodejs.org, mitigating MITM or server compromise attacks.
* **Interactive Security Interceptor**: Intercepts `npm`, `yarn`, and `pnpm` install commands to perform:
  * Vulnerability scans against the OSV database.
  * Typosquatting audits based on Levenshtein distance and registry download comparison.
  * Release-age warning for packages published less than 24 hours ago.
  * Install script blocking/warning to prevent arbitrary code execution during dependencies installation.
* **Flexible Process Sandboxing**: Runs executions inside isolated environments across platforms with selectable filesystem providers: OS-native isolation (`native`), Docker containers (`docker`), Microsoft WSL Containers via `wslc.exe` (`wslc` — Hyper-V utility VM, separate from WSL distros), default-WSL-distro fallback (`wsl`), macOS Seatbelt sandboxing (`sandbox-exec`), and Linux volatile containers (`systemd-nspawn`, requires root). Providers are selected via `isolation.filesystem.provider` or the `--filesystem-provider` shim flag, and unknown providers fail closed.
* **Project-Scoped Tool Isolation**: Optional `environment.isolated_tools` policy setting scopes globally installed npm packages to `<project>/.nvx/npm_global`, so different projects can pin different versions of global CLI tools.
* **Fail-Closed Prompts**: Security prompts deny by default in non-interactive environments (including CI); approval requires an explicit `-y` / `--yes` or `NVX_YES=true`.
* **CI Integration**: Added remote GitHub Actions CI pipeline testing across Windows, macOS, and Linux matrix with `gosec` and `govulncheck` static analysis scanners.
