package nvx

import "strings"

// dockerLaunchLine renders a `docker run` argument list for a log line: every
// environment variable by name, none by value.
//
// dockerRunArgs hands each allowed variable to docker as `-e KEY=VALUE`, and
// the launcher logged the argument list whole. LogInfo also goes to debug.log,
// and from there into the `nvx report` bundle people are asked to attach to a
// bug report. The values are exactly the ones the scrub let through on
// purpose: PATH, and whatever isolation.environment.allow names -- which is
// the mechanism for handing a token to a tool that needs one. A token a user
// deliberately allowed into the sandbox was therefore printed to the terminal
// and kept on disk. Measured on the installed build with a sentinel in PATH:
// the sentinel appeared in the printed line.
//
// Every spelling docker accepts is covered -- `-e K=V`, `--env K=V`, `-e=K=V`,
// `--env=K=V` -- so a change to how dockerRunArgs passes the environment
// cannot reopen this quietly. A bare `-e KEY` (docker copies the parent's
// value) has nothing to redact.
func dockerLaunchLine(args []string) string {
	out := make([]string, 0, len(args))
	redact := func(kv string) string {
		if i := strings.Index(kv, "="); i >= 0 {
			return kv[:i] + "=<redacted>"
		}
		return kv
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--env":
			out = append(out, a)
			if i+1 < len(args) {
				i++
				out = append(out, redact(args[i]))
			}
		case strings.HasPrefix(a, "-e=") || strings.HasPrefix(a, "--env="):
			flag, kv, _ := strings.Cut(a, "=")
			out = append(out, flag+"="+redact(kv))
		default:
			out = append(out, a)
		}
	}
	return strings.Join(out, " ")
}
