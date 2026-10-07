package nvx

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// How npm reads its command.
//
// npm takes its command from the first positional and resolves it with deref,
// in lib/utils/cmd-list.js (lib/npm.js until 9.6). A camelCase name is read
// as its hyphenated form, an alias names its command, and a prefix that
// matches one command or alias and nothing else is that one. So `npm exe` is
// exec, `npm cre` is create, which is init, and `npm installTest` is
// install-test. nvx matched a fixed list of spellings, and every other one ran
// as your own code, with no sandbox and no pre-install checks. Measured
// 2026-10-07 with npm 11.19.0 in a Linux container, `nvx npm exe --yes
// --package=cowsay -c "cat ~/canary.txt"` printed the canary, and the same
// command spelled `npm exec` was contained.
//
// The tables are cmd-list.js at each release tag, for every npm release
// bundled with Node.js 18 to 26 (8.6.0 to 11.19.1) and every 11.x and 12.x
// release up to 12.2.0. Those 81 releases have 14 different tables, and they
// disagree. `npm u` is update from 11.13.0 and unknown before it, and `npm ad`
// is install only in 12, where adduser is gone. A token is resolved under
// every table, and nvx takes the most cautious answer.

// npmCmdList is one release's command list and aliases.
type npmCmdList struct {
	since    string
	commands map[string]bool
	aliases  map[string]string
	// plumbing names are returned as typed and are not abbreviated. npm 8 to
	// 9.5 only.
	plumbing map[string]bool
	// legacy is deref as lib/npm.js wrote it before 9.6.3: the abbreviation
	// first, then aliases. From 9.6.3 an exact command or alias is looked up
	// before abbreviating, which only differs for `login` in npm 8.
	legacy  bool
	abbrevs map[string]string
}

// npmCmdListChange is how one release's cmd-list.js differs from the table
// before it.
type npmCmdListChange struct {
	since           string
	add, remove     []string
	setAliases      map[string]string
	removeAliases   []string
	replacePlumbing bool
	plumbing        []string
	modernDeref     bool
}

// npmBaseCommands and npmBaseAliases are npm 8.6.0's cmd-list.js.
var npmBaseCommands = []string{
	"access", "adduser", "audit", "bin", "bugs", "cache", "ci", "completion", "config",
	"dedupe", "deprecate", "diff", "dist-tag", "docs", "doctor", "edit", "exec", "explain",
	"explore", "find-dupes", "fund", "get", "help", "hook", "init", "install",
	"install-ci-test", "install-test", "link", "ll", "login", "logout", "ls", "org",
	"outdated", "owner", "pack", "ping", "pkg", "prefix", "profile", "prune", "publish",
	"rebuild", "repo", "restart", "root", "run-script", "search", "set", "set-script",
	"shrinkwrap", "star", "stars", "start", "stop", "team", "test", "token", "uninstall",
	"unpublish", "unstar", "update", "version", "view", "whoami",
}

var npmBaseAliases = map[string]string{
	"add": "install", "add-user": "adduser", "author": "owner", "c": "config",
	"cit": "install-ci-test", "clean-install": "ci", "clean-install-test": "cit",
	"create": "init", "ddp": "dedupe", "dist-tags": "dist-tag", "find": "search",
	"hlep": "help", "home": "docs", "i": "install", "ic": "ci", "in": "install",
	"info": "view", "innit": "init", "ins": "install", "inst": "install",
	"insta": "install", "instal": "install", "install-clean": "ci",
	"isnt": "install", "isnta": "install", "isntal": "install",
	"isntall": "install", "isntall-clean": "ci", "issues": "bugs",
	"it": "install-test", "la": "ll", "list": "ls", "ln": "link",
	"login": "adduser", "ogr": "org", "r": "uninstall", "rb": "rebuild",
	"remove": "uninstall", "rm": "uninstall", "rum": "run-script",
	"run": "run-script", "s": "search", "se": "search", "show": "view",
	"sit": "cit", "t": "test", "tst": "test", "udpate": "update",
	"un": "uninstall", "unlink": "uninstall", "up": "update", "upgrade": "update",
	"urn": "run-script", "v": "view", "verison": "version", "why": "explain",
	"x": "exec",
}

// npmCmdListChanges are the later tables, oldest first.
var npmCmdListChanges = []npmCmdListChange{
	{since: "8.18.0", add: []string{"query"}},
	{since: "9.2.0", remove: []string{"bin", "set-script"}, removeAliases: []string{"login"},
		replacePlumbing: true, plumbing: []string{"help-search"}},
	{since: "9.6.3", add: []string{"help-search"}, replacePlumbing: true, modernDeref: true,
		setAliases: map[string]string{"clean-install-test": "install-ci-test", "sit": "install-ci-test"}},
	{since: "10.2.0", add: []string{"sbom"}},
	{since: "11.0.0", remove: []string{"hook"}},
	{since: "11.1.0", add: []string{"undeprecate"}},
	{since: "11.4.0", add: []string{"run"}, remove: []string{"run-script"}, removeAliases: []string{"run"},
		setAliases: map[string]string{"rum": "run", "run-script": "run", "urn": "run"}},
	{since: "11.10.0", add: []string{"trust"}},
	{since: "11.13.0", setAliases: map[string]string{"u": "update"}},
	{since: "11.15.0", add: []string{"stage"}},
	{since: "11.16.0", add: []string{"approve-scripts", "deny-scripts"}},
	{since: "11.18.0", add: []string{"install-scripts"}},
	{since: "12.0.0", add: []string{"patch"}, remove: []string{"adduser", "shrinkwrap", "star", "stars", "unstar"},
		removeAliases: []string{"add-user"}},
}

var (
	npmCmdListsOnce sync.Once
	npmCmdListsAll  []npmCmdList
)

// npmCmdLists returns every table, newest first.
func npmCmdLists() []npmCmdList {
	npmCmdListsOnce.Do(func() {
		cur := npmCmdList{since: "8.6.0", commands: map[string]bool{}, aliases: map[string]string{},
			plumbing: map[string]bool{"birthday": true, "help-search": true}, legacy: true}
		for _, c := range npmBaseCommands {
			cur.commands[c] = true
		}
		for k, v := range npmBaseAliases {
			cur.aliases[k] = v
		}
		lists := []npmCmdList{cur.withAbbrevs()}
		for _, ch := range npmCmdListChanges {
			next := npmCmdList{since: ch.since, commands: map[string]bool{}, aliases: map[string]string{},
				plumbing: map[string]bool{}, legacy: cur.legacy && !ch.modernDeref}
			for c := range cur.commands {
				next.commands[c] = true
			}
			for k, v := range cur.aliases {
				next.aliases[k] = v
			}
			if !ch.replacePlumbing {
				for c := range cur.plumbing {
					next.plumbing[c] = true
				}
			}
			for _, c := range ch.plumbing {
				next.plumbing[c] = true
			}
			for _, c := range ch.add {
				next.commands[c] = true
			}
			for _, c := range ch.remove {
				delete(next.commands, c)
			}
			for _, k := range ch.removeAliases {
				delete(next.aliases, k)
			}
			for k, v := range ch.setAliases {
				next.aliases[k] = v
			}
			cur = next
			lists = append(lists, cur.withAbbrevs())
		}
		slices.Reverse(lists)
		npmCmdListsAll = lists
	})
	return npmCmdListsAll
}

func (l npmCmdList) withAbbrevs() npmCmdList {
	words := make([]string, 0, len(l.commands)+len(l.aliases))
	for c := range l.commands {
		words = append(words, c)
	}
	for k := range l.aliases {
		words = append(words, k)
	}
	l.abbrevs = npmAbbrev(words)
	return l
}

// npmAbbrev is the abbrev package npm uses. Every prefix of a word that no
// other word shares maps to that word, and every word maps to itself. A word
// that is a prefix of another word gets no shorter forms.
func npmAbbrev(words []string) map[string]string {
	list := append([]string(nil), words...)
	// abbrev sorts with JavaScript's < on strings, which for these ASCII names
	// is byte order.
	sort.Strings(list)
	out := map[string]string{}
	prev := ""
	for i, cur := range list {
		next := ""
		if i+1 < len(list) {
			next = list[i+1]
		}
		if cur == next {
			continue
		}
		nextMatches, prevMatches := true, true
		j := 0
		for ; j < len(cur); j++ {
			nextMatches = nextMatches && j < len(next) && cur[j] == next[j]
			prevMatches = prevMatches && j < len(prev) && cur[j] == prev[j]
			if !nextMatches && !prevMatches {
				j++
				break
			}
		}
		prev = cur
		if j == len(cur) {
			out[cur] = cur
			continue
		}
		for k := j; k <= len(cur); k++ {
			out[cur[:k]] = cur
		}
	}
	return out
}

// deref is npm's deref for this table. It returns the command c names, or "".
func (l npmCmdList) deref(c string) string {
	if c == "" {
		return ""
	}
	// installTest is install-test. Only ASCII capitals, as npm's /[A-Z]/.
	if strings.ContainsFunc(c, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		var b strings.Builder
		for _, r := range c {
			if r >= 'A' && r <= 'Z' {
				b.WriteByte('-')
				r += 'a' - 'A'
			}
			b.WriteRune(r)
		}
		c = b.String()
	}
	if l.legacy {
		if l.plumbing[c] {
			return c
		}
	} else {
		if l.commands[c] {
			return c
		}
		if a, ok := l.aliases[c]; ok {
			return a
		}
	}
	a := l.abbrevs[c]
	for range 4 {
		next, ok := l.aliases[a]
		if !ok {
			break
		}
		a = next
	}
	return a
}

// npmCommandNames returns every command npm releases resolve tok to, the
// newest release's answer first, or nil when none of them knows it.
func npmCommandNames(tok string) []string {
	var out []string
	for _, l := range npmCmdLists() {
		if c := l.deref(tok); c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// npmSafeCommands are the npm commands nvx runs as your own code. Some of them
// install, or fetch and run code, with certain arguments, such as `init vite`,
// `link ../dir`, `audit fix` and `pack github:u/r`, and classifyInvocation
// looks for those forms. A command not named here is contained, so one that a
// newer npm adds is contained until it is listed.
var npmSafeCommands = map[string]bool{
	"access": true, "adduser": true, "approve-scripts": true, "audit": true, "bin": true,
	"birthday": true, "bugs": true, "cache": true, "completion": true, "config": true,
	"deny-scripts": true, "deprecate": true, "diff": true, "dist-tag": true, "docs": true,
	"doctor": true, "explain": true, "explore": true, "find-dupes": true, "fund": true,
	"get": true, "help": true, "help-search": true, "hook": true, "init": true,
	"install-scripts": true, "link": true, "ll": true, "login": true, "logout": true,
	"ls": true, "org": true, "outdated": true, "owner": true, "pack": true, "ping": true,
	"pkg": true, "prefix": true, "profile": true, "publish": true, "query": true,
	"repo": true, "restart": true, "root": true, "run": true, "run-script": true,
	"sbom": true, "search": true, "set": true, "set-script": true, "shrinkwrap": true,
	"stage": true, "star": true, "stars": true, "start": true, "stop": true, "team": true,
	"test": true, "token": true, "trust": true, "undeprecate": true, "unpublish": true,
	"unstar": true, "version": true, "view": true, "whoami": true,
}

// npmCommandLine returns args with npm's command spelled in full, and whether
// the command read is one npmSafeCommands names. With no command at all, as
// in `npm` or `npm --version`, it is.
//
// The command is the first positional. A flag right before it may have taken
// it as its value, and then the next positional may be the command instead,
// as commandVerbIndex reads it. Each such token npm resolves is spelled out,
// and the reading stops at the first that is certainly the command, at the
// first one that is not a safe command, and at a script verb, after which nvx
// reads nothing.
func npmCommandLine(args []string) (read []string, safe bool) {
	read = args
	cands := npmCommandCandidates(args)
	if len(cands) == 0 {
		return read, true
	}
	copied := false
	for _, c := range cands {
		names := npmCommandNames(args[c.index])
		if len(names) == 0 {
			continue
		}
		allSafe := true
		for _, n := range names {
			allSafe = allSafe && npmSafeCommands[n]
		}
		safe = allSafe
		if names[0] != args[c.index] {
			if !copied {
				read, copied = append([]string(nil), args...), true
			}
			read[c.index] = names[0]
		}
		if c.certain || !allSafe || isRunScriptVerb(names[0]) {
			break
		}
	}
	return read, safe
}

type npmCommandCandidate struct {
	index   int
	certain bool
}

// npmCommandCandidates lists the tokens npm may read as its command. All but
// the last follow a flag that may have taken them as its value.
func npmCommandCandidates(args []string) []npmCommandCandidate {
	var out []npmCommandCandidate
	prevWasFlag := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// Before the command, "--" ends npm's flags and the next token is the
			// command. See findInstallVerbIndex.
			if i+1 < len(args) {
				out = append(out, npmCommandCandidate{index: i + 1, certain: true})
			}
			return out
		}
		if strings.HasPrefix(a, "-") {
			if flagTakesValue(a) && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				prevWasFlag = false
				continue
			}
			prevWasFlag = !strings.Contains(a, "=")
			continue
		}
		out = append(out, npmCommandCandidate{index: i, certain: !prevWasFlag})
		if !prevWasFlag {
			return out
		}
		prevWasFlag = false
	}
	return out
}
