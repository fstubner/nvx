# The audit log

nvx records its security decisions to `~/.nvx/audit.log` (`$NVX_HOME/audit.log`).
Nothing is ever sent anywhere. There is no uploader in nvx, and `nvx audit`,
`nvx audit export` and whatever you point at it read this file.

`nvx audit export` is the supported way to read it from another program. It
ships in v0.7.0.

```
nvx audit export --since 7d --event egress_deny --format csv --out evidence.csv
```

| Flag              | Meaning                                                                          |
| ----------------- | -------------------------------------------------------------------------------- |
| `--since <when>`  | An RFC3339 timestamp (`2026-09-01T00:00:00Z`) or a duration back from now (`7d`, `2w`, `12h`). |
| `--event <name>`  | Only this event. Repeat for several.                                              |
| `--format <fmt>`  | `jsonl` (default), `json`, or `csv`.                                              |
| `--out <path>`    | Write here instead of stdout.                                                     |

The command reports a line it cannot parse on stderr and exits `1`. It still
exports everything it could read and says how many records that was.
A torn line happens. Concurrent processes append records, and a crash
mid-write leaves one behind. `nvx audit` skips those silently because it is
printing to a screen. An export is evidence, and evidence that quietly omits
records is worse than none.

## The format

One JSON object per line. Every export renders every value as a **string**,
including `pid`, whatever the on-disk record used, so a consumer never has to
handle both.

nvx sanitises values on the way out. Control characters, `DEL` and `|` become
spaces, and runs of spaces collapse. The contained process itself supplies several
fields (a SOCKS5 request names its own destination host). The log is a
plain file anything on the machine can append to. A newline in a value would
otherwise forge an extra record, and an escape sequence would rewrite a terminal.
The `warnings` field keeps its ` | ` separator, which is what splits one run's
warnings apart.

### Fields on every record

| Field   | Meaning                                                        |
| ------- | -------------------------------------------------------------- |
| `time`  | RFC3339, UTC, when the record was written.                      |
| `pid`   | The nvx process that wrote it.                                  |
| `event` | The event name, from the table below.                           |
| `cwd`   | The working directory at the time. Absent if it could not be read. |

### Events

Security decisions are always recorded. The `run` and `hangup_watch` events are a
debugging aid and nvx writes them only when `NVX_TRACE=1`.

| Event                            | Extra fields                                              | Written when                                                               |
| -------------------------------- | --------------------------------------------------------- | -------------------------------------------------------------------------- |
| `egress_deny`                    | `host`                                                    | A contained process was refused a host that is not on the allowlist.        |
| `egress_deny_resolved`           | `host`                                                    | A host resolved to an address the allowlist does not cover (rebinding check). |
| `egress_deny_invalid_host`       | `host`                                                    | A destination that is not a valid host name was refused.                     |
| `egress_deny_loopback_prompt`    | `host`                                                    | A loopback destination was refused, which `NVX_TRUST_YES` cannot approve, or a name it approved resolved to loopback. |
| `egress_deny_unresolved_prompt`  | `host`                                                    | A name `NVX_TRUST_YES` approved would not resolve, so it was refused.        |
| `egress_deny_link_local_prompt`  | `host`                                                    | A link-local address (169.254.0.0/16 or fe80::/10, such as the cloud metadata address 169.254.169.254) was refused, which `NVX_TRUST_YES` cannot approve. An `allow_hosts` entry naming it still allows it. |
| `egress_block_mode`              | `host`, `mode`                                            | The network mode (`offline`, `loopback`) refused the request outright.       |
| `egress_allow_prompted`          | `host`, `by`                                              | `NVX_TRUST_YES` approved a host the allowlist does not name, for that run. nvx no longer asks, so `by` is `nvx_trust_yes`. |
| `egress_allow`                   | `host`, `rule`                                            | A contained run reached a host that the policy allows. Written once for each host and port in each run, at its first connection. `rule` is `allow_hosts` when the host is listed there, `default_allow` for the shipped hosts and a runtime's own, and `mode_loopback` when `network.mode` is `loopback`. A host `NVX_TRUST_YES` approved has `egress_allow_prompted` instead. |
| `trusted_tool_granted`           | `tool`, `project`, `by`                                   | A tool was granted a persistent sandbox profile for this project. `by` is `nvx_trust` (`nvx trust --tool`) or `nvx_trust_yes`. |
| `trusted_tool_denied`            | `tool`, `project`                                         | nvx refused that grant, and the command did not run.                         |
| `trusted_tool_grant_persist_failed` | `tool`, `project`                                      | The grant was approved and could not be written down.                        |
| `policy_pin_accepted`            | `path`, `by`                                              | A loosening project policy file was trusted for this project. `by` is `nvx_trust`, `nvx_allow_host` or `nvx_trust_yes`. |
| `policy_pin_changed_denied`      | `path`                                                    | nvx refused to run under a loosening project policy file nobody had trusted at its current content. |
| `allow_host_added`               | `host`, `path`                                            | `nvx allow-host` added a host to `isolation.network.allow_hosts` in that policy file. |
| `allow_host_removed`             | `host`, `path`                                            | `nvx allow-host --remove` took a host out of `isolation.network.allow_hosts` in that policy file. |
| `install_scripts_exempt`         | `package`, `version`                                      | A package's install scripts ran without asking, per `install_scripts.trusted_packages`. |
| `vulnerability_allowed`          | `package`, `advisory`, `severity`, `reason`               | A known advisory did not stop an install. `reason` is `allowlisted` or `below_min_severity`. |
| `check_approved`                 | `check`, `by`, and optionally `package`, `version`, `detail` | A pre-install check went ahead. `check` is `typosquat`, `release_age`, `install_scripts`, `vulnerability`, `registry_unreachable`, `osv_unreachable`, `resolution_failed` (npm could not say what an install brings in) or `lockfile_unreadable`. A `release_age` record whose `detail` is `publish time unknown` is for a version the registry gave no publish time for. `by` is `yes_flag` or `nvx_yes` when nobody was asked (stderr also prints a line), or `prompt` when a person answered. Until `--agent-mode` was made to refuse, it could also be `agent_mode`. |
| `check_refused`                  | `check`, `by`, and optionally `package`, `version`, `detail` | A pre-install check stopped the install. `check` is one of the above, or `blocked_package`, `enforce_ignore_scripts`, `lockfile_mismatch` (a lockfile entry's tarball URL or hash is not the registry's for its name and version) or `malicious_package` (OSV lists the package as malicious, `detail` holds the `MAL-` IDs). `by` is `prompt`, `non_interactive` (nobody was there to answer), `agent_mode` (`--agent-mode` or `NVX_AGENT_MODE` refused it without asking) or `policy` (no prompt exists for it). |
| `check_skipped`                  | `check`, `by`, `detail`                                   | Some checks did not run for part of an install. `check` is `public_registry_checks`: packages from a registry other than registry.npmjs.org got no typosquat or advisory check, because both send the name to a public service. `by` is `registry`, and `detail` gives the count and the registry hosts. Written once per run. `check` is `install_scripts` when a package with install scripts was not asked about or refused, because the command turns scripts off. `by` is `ignore_scripts_flag`, `ignore_scripts_env`, `ignore_scripts_npmrc`, `yarn_mode_skip_build` or `yarnrc_enable_scripts`, and `detail` names the source. Written once per run. |
| `env_scrubbed`                   | `count`, `dropped`                                        | Containment dropped environment variables that a build might have wanted. `dropped` is comma-separated. |
| `env_pass_refused`               | `names`                                                   | `isolation.environment.allow` named a variable that holds a credential by convention, and it was not passed in. `names` is comma-separated. |
| `sandbox_not_started`            | `command`, `reason`                                       | nvx declined to run a command, or could not establish the containment it promises. |
| `connect_peer_refused`           | `host_port`, `reason`                                     | A connection to a published port came from outside this sandbox. `reason` is `not_in_this_sandbox` or `unverifiable`. |
| `loopback_redirect`              | `address`                                                 | A contained process reached a host service through the loopback tunnel.      |
| `loopback_redirect_refused`      | `address`                                                 | It asked for a non-loopback address through that tunnel and was refused.     |
| `npm_global_override_used`       | `path`, `cmd`                                             | A project-local npm global binary was used ahead of the system one.          |
| `hangup_watch`                   | `state`, `reason`                                         | (Trace only, Windows) The parent-process watchdog changed state.             |
| `run`                            | `command`, `mode`, `exit`, `duration_ms`, and optionally `action`, `reason`, `warnings` | (Trace only) One command finished. |

`warnings` holds each warning's **format string**, never its rendered text, because
rendering one put a live password in the log once. `nvx audit` replaces the printf
verbs with `[…]` on the way out. The export does not, so a consumer sees exactly
what nvx stored.

In `csv` only, a value that starts with `=`, `+`, `-`, `@`, a tab or a carriage
return, and is not a plain number, gets a leading `'`. Spreadsheets run such a cell
as a formula, and the log is a file anything on the machine can append to.

Arguments are never recorded. `action` holds only a subcommand nvx recognises by
name (`install`, `run`, `add`). This is because a package spec or a script name can
carry a registry token or an internal project name.

## The stability guarantee

This is what a pipeline built on the export can rely on.

**Will not change without a major version:**

- The four fields on every record are `time`, `pid`, `event`, `cwd`.
- `time` is RFC3339 in UTC.
- One JSON object per line in `jsonl`, and every value a string in every format.
- The event names in the table above, and the meaning of each.
- The fields listed above for each of those events.

**May change in any release:**

- New event names, and new fields on an existing event. Treat an unknown event or
  an unexpected field as data to keep, not as an error.
- The human-readable text inside a field: `reason`, `warnings`, and the contents
  of `dropped` are messages, not identifiers. Do not match on them. Match on the
  event name and the field that carries the identifier.
- The order of CSV columns after the leading four (`time`, `pid`, `event`, `cwd`),
  which is the sorted union of whatever the selected records carry. Read a CSV by
  its header row.
- Whether nvx writes a given event at all under a given setting, and the rotation
  size.

**Not a guarantee at all.** The log is a local file with a size cap. nvx keeps one
previous generation (`audit.log.1`) and discards older records. A pipeline
that needs to keep them must export on a schedule, or copy the file.
