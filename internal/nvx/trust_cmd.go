package nvx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// `nvx trust` and `nvx allow-host` record the decisions nvx no longer takes at a
// prompt. They are what a refusal to widen the sandbox tells a person to run, in
// their own terminal. See trust_boundary.go.

func runTrust(args []string, nvxHome string) int {
	if wantsHelp(args) {
		fmt.Print(commandHelpText("trust"))
		return 0
	}
	var file, tool, hash string
	toolGiven := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--tool" && i+1 < len(args):
			tool, toolGiven = args[i+1], true
			i++
		case strings.HasPrefix(a, "--tool="):
			tool, toolGiven = strings.TrimPrefix(a, "--tool="), true
		case a == "--hash" && i+1 < len(args):
			hash = args[i+1]
			i++
		case strings.HasPrefix(a, "--hash="):
			hash = strings.TrimPrefix(a, "--hash=")
		case !strings.HasPrefix(a, "-") && file == "":
			file = a
		default:
			LogError("Usage: nvx trust [<policy-file> [--hash <hash>]] | nvx trust --tool <name>")
			return 1
		}
	}
	switch {
	case toolGiven && file == "" && hash == "":
		return trustTool(nvxHome, tool)
	case toolGiven, hash != "" && file == "":
		LogError("Usage: nvx trust [<policy-file> [--hash <hash>]] | nvx trust --tool <name>")
		return 1
	}
	return trustProjectPolicies(nvxHome, file, hash)
}

// trustHashLen is how much of a policy file's hash a refusal prints for
// `nvx trust --hash`: 64 bits, too many to find a second file to match.
const trustHashLen = 16

// trustProjectPolicies records trust for the project policy files that apply in
// this folder, loosen settings and are not trusted at their current content:
// the one named, or with none named, every such file. It pins the bytes it read.
//
// hash, when given, is the start of the hash a refusal printed, and the file is
// trusted only if it still has that content. A prompt pinned exactly the bytes
// it showed. A refusal shows them first, and the file can change before the
// person runs this, which contained code that writes the project can arrange.
func trustProjectPolicies(nvxHome, target, hash string) int {
	cwd, err := os.Getwd()
	if err != nil {
		LogError("Could not determine the current folder: %v", err)
		return 1
	}
	want := ""
	if target != "" {
		abs, err := filepath.Abs(target)
		if err != nil {
			LogError("%v", err)
			return 1
		}
		want = canonicalPath(abs)
	}
	if hash != "" && len(hash) < trustHashLen {
		LogError("--hash needs the %d characters nvx printed.", trustHashLen)
		return 1
	}
	grants := loadProjectGrants(nvxHome, projectScopeDir())
	var trusting []projectPolicyStep
	found := false
	_, err = walkProjectPolicies(nvxHome, cwd, grants, func(s projectPolicyStep) bool {
		named := want == "" || dirsEqual(canonicalPath(s.path), want)
		found = found || named
		if len(s.loosenings) == 0 || s.trusted {
			return true
		}
		if !named {
			// Not trusted and not asked about, so it does not apply, and the
			// files nearer this folder are compared without it, as LoadPolicy does.
			return false
		}
		trusting = append(trusting, s)
		return true
	})
	if err != nil {
		LogError("%v", err)
		return 1
	}
	if want != "" && !found {
		LogError("%s is not a project policy file that applies in %s.", want, cwd)
		LogInfo("Run nvx trust in the folder where nvx refused. A project policy file applies in its own folder and every folder below it.")
		return 1
	}
	if len(trusting) == 0 {
		LogSuccess("Nothing to trust here. Every project policy file that applies in %s is in force already.", cwd)
		return 0
	}
	if hash != "" && !strings.HasPrefix(trusting[0].hash, strings.ToLower(hash)) {
		LogError("%s has changed since nvx refused it, so it was not trusted. It now loosens:", trusting[0].path)
		for _, line := range loosenedSettings(trusting[0].loosenings) {
			LogRefusalDetail("    %s", line)
		}
		LogRefusalDetail("Read the file. To trust what it says now, run nvx trust %s", policyFileArg(cwd, trusting[0].path))
		return 1
	}
	pins := map[string]string{}
	for _, s := range trusting {
		pins[s.path] = s.hash
	}
	if err := recordPolicyPins(nvxHome, pins); err != nil {
		LogError("Could not record the trust: %v", err)
		return 1
	}
	for _, s := range trusting {
		auditLog(nvxHome, "policy_pin_accepted", map[string]string{"path": s.path, "by": "nvx_trust"})
		LogSuccess("Trusted %s, wherever it applies. It loosens:", s.path)
		for _, line := range loosenedSettings(s.loosenings) {
			LogRefusalDetail("    %s", line)
		}
	}
	LogInfo("If the file changes, nvx refuses it again until it is trusted again. nvx grants reset, in the folder that holds it, forgets this.")
	return 0
}

// trustTool lets a tool keep a persistent profile in this project.
func trustTool(nvxHome, name string) int {
	tool := strings.ToLower(stripVersionSuffix(strings.TrimSpace(name)))
	if safePackageLabel(tool) == "" {
		LogError("%q is not a tool name. Give the name nvx printed, for example: nvx trust --tool wrangler", name)
		return 1
	}
	scope := projectScopeDir()
	if scope == "" {
		LogError("Could not determine the current project.")
		return 1
	}
	if err := recordTrustedTool(nvxHome, scope, tool); err != nil {
		LogError("Could not record the trust: %v", err)
		return 1
	}
	auditLog(nvxHome, "trusted_tool_granted", map[string]string{"tool": tool, "project": scope, "by": "nvx_trust"})
	LogSuccess("%s may keep a persistent profile in %s, so its logins and settings last between runs. It is still contained.", tool, scope)
	LogInfo("nvx grants reset forgets this.")
	return 0
}

const allowHostUsage = "Usage: nvx allow-host [--remove] <host[:port]> [--project | --global]"

func runAllowHost(args []string, nvxHome string) int {
	if wantsHelp(args) {
		fmt.Print(commandHelpText("allow-host"))
		return 0
	}
	global, project, remove := false, false, false
	host := ""
	for _, a := range args {
		switch {
		case a == "--global":
			global = true
		case a == "--project":
			project = true
		case a == "--remove":
			remove = true
		case strings.HasPrefix(a, "-") || host != "":
			LogError(allowHostUsage)
			return 1
		default:
			host = a
		}
	}
	if host == "" || (global && project) {
		LogError(allowHostUsage)
		return 1
	}
	entry, err := allowHostEntry(host)
	if err != nil {
		LogError("%v", err)
		return 1
	}
	switch {
	case remove && global:
		return removeHostGlobally(nvxHome, entry)
	case remove:
		return removeHostInProject(nvxHome, entry)
	case global:
		return allowHostGlobally(nvxHome, entry)
	}
	return allowHostInProject(nvxHome, entry)
}

// allowHostEntry turns host[:port] into an allow_hosts entry in the form the
// egress proxy compares, host:port. The port defaults to 443, the narrowest
// useful answer. A bare host in a policy file means every port, which is wider
// than someone typing a host name usually means. host:* still says so.
func allowHostEntry(arg string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(arg))
	host, port := s, "443"
	switch {
	case strings.HasPrefix(s, "["):
		h, p, err := net.SplitHostPort(s)
		if err != nil {
			return "", fmt.Errorf("%q is not host:port: %v", arg, err)
		}
		host, port = h, p
	case strings.Count(s, ":") == 1:
		host, port = s[:strings.Index(s, ":")], s[strings.Index(s, ":")+1:]
	case strings.Count(s, ":") > 1:
		// An IPv6 address, in the egress proxy's own form, with the port last.
		i := strings.LastIndex(s, ":")
		if net.ParseIP(s[:i]) == nil {
			return "", fmt.Errorf("%q is not host:port. Write an IPv6 address in brackets, as [::1]:443", arg)
		}
		host, port = s[:i], s[i+1:]
	}
	if !validEgressHost(host) {
		return "", fmt.Errorf("%q is not a host name or an IP address", host)
	}
	if port != "*" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%q is not a port. Give a number from 1 to 65535, or * for every port", port)
		}
	}
	return host + ":" + port, nil
}

// allowHostGlobally adds entry to ~/.nvx/policy.json, which needs no trust
// because it is the person's own file, outside every project.
func allowHostGlobally(nvxHome, entry string) int {
	path := filepath.Join(nvxHome, "policy.json")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		LogError("Could not read %s: %v", path, err)
		return 1
	}
	out, added, err := withAllowedHost(data, entry)
	if err != nil {
		LogError("Could not update %s: %v", path, err)
		return 1
	}
	if !added {
		LogSuccess("%s is already in isolation.network.allow_hosts in %s.", entry, path)
		return 0
	}
	// What is written must still read as the policy it was.
	policy := DefaultPolicy()
	if err := json.Unmarshal(out, &policy); err != nil {
		LogError("Could not update %s: %v", path, err)
		return 1
	}
	if err := writeFileReplacing(path, out); err != nil {
		LogError("Could not write %s: %v", path, err)
		return 1
	}
	auditLog(nvxHome, "allow_host_added", map[string]string{"host": entry, "path": path})
	LogSuccess("Added %s to isolation.network.allow_hosts in %s. Every project may reach it now.", entry, path)
	return 0
}

// allowHostInProject adds entry to this project's policy file and trusts the
// result, so one command does what the refusal asked for.
//
// Only when the file's other loosenings are already trusted. Trusting the file
// pins all of it, and a person who asked for one host has not read the rest.
func allowHostInProject(nvxHome, entry string) int {
	cwd, err := os.Getwd()
	if err != nil {
		LogError("Could not determine the current folder: %v", err)
		return 1
	}
	scope := projectScopeDir()
	if home, herr := os.UserHomeDir(); herr == nil && dirsEqual(scope, home) {
		// A policy file in the home folder applies to every project below it.
		LogError("%s is your home folder, not a project. Run this in the project, or pass --global to allow the host everywhere.", scope)
		return 1
	}
	target := projectPolicyFileFor(cwd, scope, nvxHome)
	grants := loadProjectGrants(nvxHome, scope)
	var targetStep *projectPolicyStep
	final, err := walkProjectPolicies(nvxHome, cwd, grants, func(s projectPolicyStep) bool {
		if dirsEqual(s.path, target) {
			step := s
			targetStep = &step
		}
		return len(s.loosenings) == 0 || s.trusted
	})
	if err != nil {
		LogError("%v", err)
		return 1
	}

	over := final
	var data []byte
	if targetStep != nil {
		if len(targetStep.loosenings) > 0 && !targetStep.trusted {
			LogError("%s loosens other settings too, and they have not been trusted for this project:", target)
			for _, line := range loosenedSettings(targetStep.loosenings) {
				LogRefusalDetail("    %s", line)
			}
			LogRefusalDetail("Read the file. If you mean all of it, run nvx trust %s first, then this again.", policyFileArg(cwd, target))
			return 1
		}
		over = targetStep.over
		if data, err = os.ReadFile(target); err != nil {
			LogError("Could not read %s: %v", target, err)
			return 1
		}
		// The bytes edited and pinned are the bytes just checked. Contained code
		// can write the project, and a file swapped between the two reads would
		// have its other loosenings trusted along with the host.
		if hashPolicyBytes(data) != targetStep.hash {
			LogError("%s changed while nvx was reading it, so nothing was written. Run this again.", target)
			return 1
		}
	}

	out, added, err := withAllowedHost(data, entry)
	if err != nil {
		LogError("Could not update %s: %v", target, err)
		return 1
	}
	if !added {
		LogSuccess("%s is already in isolation.network.allow_hosts in %s.", entry, target)
		return 0
	}
	local, _, err := parseProjectPolicyBytes(target, out)
	if err != nil {
		LogError("Could not update %s: %v", target, err)
		return 1
	}
	// An enforced global policy refuses a project file that adds a host. Said
	// before anything is written, rather than leaving a file every command
	// refuses.
	if _, err := MergeUnderBaseline(over, local, target); err != nil {
		LogError("%v", err)
		LogInfo("The global policy is enforced, so a host is added there: nvx allow-host %s --global", entry)
		return 1
	}
	if err := writeFileReplacing(target, out); err != nil {
		LogError("Could not write %s: %v", target, err)
		return 1
	}
	if err := recordPolicyPins(nvxHome, map[string]string{target: hashPolicyBytes(out)}); err != nil {
		LogError("Added %s to %s, but could not record that it is trusted: %v", entry, target, err)
		LogInfo("Run nvx trust %s to trust it.", policyFileArg(cwd, target))
		return 1
	}
	auditLog(nvxHome, "allow_host_added", map[string]string{"host": entry, "path": target})
	auditLog(nvxHome, "policy_pin_accepted", map[string]string{"path": filepath.Clean(target), "by": "nvx_allow_host"})
	LogSuccess("Added %s to isolation.network.allow_hosts in %s, and trusted that file wherever it applies.", entry, target)
	return 0
}

// removeHostGlobally takes entry out of ~/.nvx/policy.json, which is the undo
// for `nvx allow-host --global`.
func removeHostGlobally(nvxHome, entry string) int {
	path := filepath.Join(nvxHome, "policy.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hostNotListed(entry, path, false, false)
	}
	if err != nil {
		LogError("Could not read %s: %v", path, err)
		return 1
	}
	out, removed, err := withoutAllowedHost(data, entry)
	if err != nil {
		LogError("Could not update %s: %v", path, err)
		return 1
	}
	if !removed {
		return hostNotListed(entry, path, false, true)
	}
	policy := DefaultPolicy()
	if err := json.Unmarshal(out, &policy); err != nil {
		LogError("Could not update %s: %v", path, err)
		return 1
	}
	if err := writeFileReplacing(path, out); err != nil {
		LogError("Could not write %s: %v", path, err)
		return 1
	}
	auditLog(nvxHome, "allow_host_removed", map[string]string{"host": entry, "path": path})
	LogSuccess("Removed %s from isolation.network.allow_hosts in %s.", entry, path)
	return 0
}

// removeHostInProject takes entry out of this project's policy file, which is
// the undo for `nvx allow-host`.
//
// Taking a host out only narrows the file, so a file that was trusted as it
// stood stays trusted, and the pin moves to the new content. A file nobody had
// trusted is not pinned here, because that would trust whatever else it says.
func removeHostInProject(nvxHome, entry string) int {
	cwd, err := os.Getwd()
	if err != nil {
		LogError("Could not determine the current folder: %v", err)
		return 1
	}
	scope := projectScopeDir()
	target := projectPolicyFileFor(cwd, scope, nvxHome)
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return hostNotListed(entry, target, true, false)
	}
	if err != nil {
		LogError("Could not read %s: %v", target, err)
		return 1
	}
	out, removed, err := withoutAllowedHost(data, entry)
	if err != nil {
		LogError("Could not update %s: %v", target, err)
		return 1
	}
	if !removed {
		return hostNotListed(entry, target, true, true)
	}
	trusted := policyPinned(nvxHome, loadProjectGrants(nvxHome, scope), target, hashPolicyBytes(data))
	if _, _, err := parseProjectPolicyBytes(target, out); err != nil {
		LogError("Could not update %s: %v", target, err)
		return 1
	}
	if err := writeFileReplacing(target, out); err != nil {
		LogError("Could not write %s: %v", target, err)
		return 1
	}
	auditLog(nvxHome, "allow_host_removed", map[string]string{"host": entry, "path": target})
	LogSuccess("Removed %s from isolation.network.allow_hosts in %s.", entry, target)
	if !trusted {
		return 0
	}
	if err := recordPolicyPins(nvxHome, map[string]string{target: hashPolicyBytes(out)}); err != nil {
		LogWarn("Could not record that the narrower file is still trusted: %v", err)
		LogInfo("If nvx refuses it, run nvx trust %s.", policyFileArg(cwd, target))
		return 1
	}
	auditLog(nvxHome, "policy_pin_accepted", map[string]string{"path": filepath.Clean(target), "by": "nvx_allow_host"})
	return 0
}

// hostNotListed answers a removal of a host the file does not list. Nothing is
// wrong, so it exits 0, as allowing a host twice does.
func hostNotListed(entry, path string, project, fileExists bool) int {
	if fileExists {
		LogInfo("%s is not in isolation.network.allow_hosts in %s, so there is nothing to remove.", entry, path)
	} else {
		LogInfo("There is no %s, so there is nothing to remove %s from.", path, entry)
	}
	if project {
		LogInfo("A host allowed for every project is in ~/.nvx/policy.json. Add --global to remove it from there.")
	}
	return 0
}

// projectPolicyFileFor is the file `nvx allow-host` writes: the nearest project
// policy file between cwd and the project root, or a new .nvx-policy.json at the
// root. A file above the project root is shared with other projects, and is not
// this command's to change.
func projectPolicyFileFor(cwd, scope, nvxHome string) string {
	for _, path := range collectProjectPolicyPaths(cwd, nvxHome) {
		if dirWithin(filepath.Dir(path), scope) {
			return filepath.Clean(path)
		}
	}
	return filepath.Join(scope, ".nvx-policy.json")
}

// writeFileReplacing writes data to path through a temporary file and a rename,
// so a run reading the policy at the same moment sees the old file or the new
// one, and never half of either.
func writeFileReplacing(path string, data []byte) error {
	// Through a symlink, the file it points at is the one to replace. Renaming
	// over the link would turn a dotfiles-managed ~/.nvx/policy.json into a copy.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// A file that exists keeps its permissions. The temporary file is created
	// readable by its owner only, which suits a new policy file and would
	// quietly change a project file others read.
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, info.Mode().Perm())
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// withAllowedHost returns a policy file's bytes with entry added to
// isolation.network.allow_hosts, and whether it was added. Every other key keeps
// its place, so the change reads as one line in a diff. A policy file is
// reviewed in diffs, and rewriting it in sorted key order would bury the one
// line that matters.
func withAllowedHost(data []byte, entry string) ([]byte, bool, error) {
	data = withoutUTF8BOM(data)
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	root, err := parseOrderedObject(data)
	if err != nil {
		return nil, false, err
	}
	isolation, err := root.object("isolation")
	if err != nil {
		return nil, false, err
	}
	network, err := isolation.object("network")
	if err != nil {
		return nil, false, err
	}
	var hosts []string
	if raw, ok := network.vals["allow_hosts"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &hosts); err != nil {
			return nil, false, fmt.Errorf("isolation.network.allow_hosts is not a list of strings: %v", err)
		}
	}
	for _, h := range hosts {
		if strings.EqualFold(strings.TrimSpace(h), entry) {
			return data, false, nil
		}
	}
	if hosts == nil {
		hosts = []string{}
	}
	hostsJSON, err := json.Marshal(append(hosts, entry))
	if err != nil {
		return nil, false, err
	}
	network.set("allow_hosts", hostsJSON)
	isolation.set("network", network.encode())
	root.set("isolation", isolation.encode())

	var out bytes.Buffer
	if err := json.Indent(&out, root.encode(), "", "  "); err != nil {
		return nil, false, err
	}
	out.WriteByte('\n')
	return out.Bytes(), true, nil
}

// withoutAllowedHost returns a policy file's bytes with entry taken out of
// isolation.network.allow_hosts, and whether it was there. It is withAllowedHost
// the other way round, and keeps the file as it was in the same ways. A list
// left empty stays in the file as [].
func withoutAllowedHost(data []byte, entry string) ([]byte, bool, error) {
	data = withoutUTF8BOM(data)
	if len(bytes.TrimSpace(data)) == 0 {
		return data, false, nil
	}
	root, err := parseOrderedObject(data)
	if err != nil {
		return nil, false, err
	}
	isolation, err := root.object("isolation")
	if err != nil {
		return nil, false, err
	}
	network, err := isolation.object("network")
	if err != nil {
		return nil, false, err
	}
	raw, ok := network.vals["allow_hosts"]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return data, false, nil
	}
	var hosts []string
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, false, fmt.Errorf("isolation.network.allow_hosts is not a list of strings: %v", err)
	}
	kept := []string{}
	for _, h := range hosts {
		if !strings.EqualFold(strings.TrimSpace(h), entry) {
			kept = append(kept, h)
		}
	}
	if len(kept) == len(hosts) {
		return data, false, nil
	}
	keptJSON, err := json.Marshal(kept)
	if err != nil {
		return nil, false, err
	}
	network.set("allow_hosts", keptJSON)
	isolation.set("network", network.encode())
	root.set("isolation", isolation.encode())

	var out bytes.Buffer
	if err := json.Indent(&out, root.encode(), "", "  "); err != nil {
		return nil, false, err
	}
	out.WriteByte('\n')
	return out.Bytes(), true, nil
}

// orderedObject is a JSON object that keeps the order of its keys.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseOrderedObject(data []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	o := &orderedObject{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a key, found %v", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("unexpected data after the JSON object")
	}
	return o, nil
}

// object returns the object under key, parsed, or a new empty one when the key
// is absent or null.
func (o *orderedObject) object(key string) (*orderedObject, error) {
	raw, ok := o.vals[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return &orderedObject{vals: map[string]json.RawMessage{}}, nil
	}
	child, err := parseOrderedObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", key, err)
	}
	return child, nil
}

// set replaces the value under key, or adds it at the end.
func (o *orderedObject) set(key string, raw json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

func (o *orderedObject) encode() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}
