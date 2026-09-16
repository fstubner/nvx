# Exit codes

`nvx policy check` is meant to gate a pipeline, so it exits with a different code
for each kind of failure. Every other nvx command exits `0` for success and `1`
for anything else, and that is unchanged.

## `nvx policy check`

| Code | Class                 | Means                                                                                     |
| ---- | --------------------- | ----------------------------------------------------------------------------------------- |
| 0    | (pass)                | Every check that ran found nothing.                                                        |
| 1    | `internal_error`      | nvx could not finish: an unknown flag, an unreadable home, a registry or OSV lookup that failed. No verdict was reached. |
| 10   | `policy_violation`    | A project policy file is not in force: it loosens an enforced global baseline and was refused, or it loosens settings and has never been trusted. |
| 11   | `blocked_package`     | A package named in `blocked_packages` is a dependency of this project.                      |
| 12   | `vulnerability`       | An advisory at or above `vulnerabilities.min_severity` that `vulnerabilities.allowed_advisories` does not accept. Needs `--online`. |
| 13   | `release_age`         | A direct dependency was published inside the `release_age` cooling-off window. Needs `--online`. |
| 14   | `sandbox_unavailable` | `isolation.enabled` is true and this platform has no OS-native sandbox, so every contained command will refuse to run. |
| 15   | `policy_file_invalid` | A policy file could not be read or parsed, so no verdict could be reached about the project. |

When more than one class fails, all of them are reported and the exit code is the
most severe present, in the order of the table: `internal_error` first, then
`policy_file_invalid`, then down to `sandbox_unavailable`. A run that could not
finish outranks a verdict because it did not reach one; a platform that cannot
contain is last because it is a property of the machine rather than of the change
under review.

`--format=json` prints the same verdict as data:

```json
{
  "ok": false,
  "exit_code": 11,
  "findings": [
    { "class": "blocked_package", "code": 11, "message": "left-pad is named in blocked_packages and is a dependency of this project" }
  ],
  "skipped": ["known vulnerabilities and release age (both need the network; pass --online)"],
  "checked": ["policy files parse", "project policy files are in force", "containment is available on this platform", "dependencies against blocked_packages (from package.json)"]
}
```

`checked` and `skipped` are part of the output on purpose: a green result that
skipped the vulnerability scan is not the same claim as a green result that ran
it, and a consumer should not have to infer which happened from the flags it
passed.

## Stability

These numbers are a contract. A class may be added, and the existing numbers will
not be reassigned. `0` and `1` keep the meanings they have everywhere else in nvx,
so a pipeline that only distinguishes zero from non-zero is unaffected by any of
this.

`TestExitCodesMatchTheirDocumentation` compares this table against the constants
in `internal/nvx/exit_codes.go`, so the two cannot drift apart silently.

## Behaviour in CI

`nvx policy check` never prompts. A prompt in a pipeline is a hang, and a hang is
worse than a failure because nothing reports it until the job times out. An
untrusted project policy file is reported as a finding rather than asked about.

It makes no network request unless `--online` is passed. Reaching the npm registry
and OSV turns the check into something that fails when a third party is down,
which in a pipeline looks exactly like a real violation. The checks that need the
network say they were skipped rather than passing silently.
