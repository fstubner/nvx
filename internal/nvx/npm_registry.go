package nvx

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Which registry a package comes from, as npm would decide it.
//
// The pre-install checks asked registry.npmjs.org about every package whatever
// registry the project used. Measured 2026-10-01 against main, a scoped package
// served only by a private registry came back 404, and with nobody to answer the
// prompt the install was refused. nvx now reads the registry npm will use for
// each package and asks that one.
//
// What npm reads depends on how it runs. A contained npm gets a guest home and
// none of the npm_config_* variables (see sensitiveEnvPrefixes), so it reads the
// project's .npmrc and nothing else. Checking a contained install against a
// scope mapping that only ~/.npmrc holds would check the private package while
// npm fetched the public one of the same name. An uncontained npm reads
// npm_config_* first, then the project's .npmrc, then the user's.

// publicNpmRegistry is the registry npm uses when nothing says otherwise. A
// variable so a test can stand a local server in for it.
var publicNpmRegistry = "https://registry.npmjs.org/"

// npmRegistryConfig is the part of npm's configuration the checks need.
type npmRegistryConfig struct {
	registry string            // the default registry, "" for the public one
	scopes   map[string]string // "@corp" -> registry URL
	// tokens maps npm's host-and-path key ("//npm.corp/") to an _authToken.
	// They are used only by nvx's own metadata requests, which run outside the
	// sandbox. Nothing here is put in an environment or written to a log.
	tokens map[string]string
}

// loadNpmRegistryConfig reads the registry settings npm will use for a project.
// contained says whether npm runs inside the sandbox, where it reads only the
// project's .npmrc. Tokens are read from the user's .npmrc in both cases,
// because nvx's own requests are not contained.
func loadNpmRegistryConfig(projectDir string, contained bool) npmRegistryConfig {
	c := npmRegistryConfig{scopes: map[string]string{}, tokens: map[string]string{}}
	userFile := userNpmrcPath(contained)
	user := readNpmrc(userFile)
	project := map[string]string{}
	if projectDir != "" {
		projectFile := filepath.Join(projectDir, ".npmrc")
		if !sameFile(projectFile, userFile) {
			project = readNpmrc(projectFile)
		}
	}

	// Lowest precedence first, so a later source overwrites an earlier one.
	sources := []map[string]string{}
	if !contained {
		sources = append(sources, user)
	}
	sources = append(sources, project)
	if !contained {
		sources = append(sources, npmConfigFromEnv())
	}
	for _, src := range sources {
		c.apply(src, true)
	}
	if contained {
		// Tokens only, from the user's file. Its registry lines do not apply to
		// a contained npm.
		c.apply(user, false)
		c.apply(project, false)
	}
	return c
}

func (c *npmRegistryConfig) apply(settings map[string]string, registries bool) {
	for k, v := range settings {
		switch {
		case strings.HasPrefix(k, "//") && strings.HasSuffix(k, ":_authToken"):
			if v != "" {
				c.tokens[strings.TrimSuffix(k, ":_authToken")] = v
			}
		case !registries:
		case k == "registry":
			c.registry = v
		case strings.HasPrefix(k, "@") && strings.HasSuffix(k, ":registry"):
			c.scopes[strings.ToLower(strings.TrimSuffix(k, ":registry"))] = v
		}
	}
}

// userNpmrcPath is the user's .npmrc, or "" for a contained npm, which does not
// read it. npm_config_userconfig moves it, as it does for npm.
func userNpmrcPath(contained bool) string {
	if !contained {
		if p := envCaseInsensitive("npm_config_userconfig"); p != "" {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".npmrc")
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// envCaseInsensitive reads an environment variable the way npm reads its own,
// whatever the case of the name.
func envCaseInsensitive(name string) string {
	for _, e := range os.Environ() {
		k, v, ok := strings.Cut(e, "=")
		if ok && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// npmConfigFromEnv collects npm_config_registry and npm_config_@scope:registry.
func npmConfigFromEnv() map[string]string {
	out := map[string]string{}
	for _, e := range os.Environ() {
		k, v, ok := strings.Cut(e, "=")
		if !ok || len(k) <= len("npm_config_") || !strings.EqualFold(k[:len("npm_config_")], "npm_config_") {
			continue
		}
		key := strings.ToLower(k[len("npm_config_"):])
		if key == "registry" || (strings.HasPrefix(key, "@") && strings.HasSuffix(key, ":registry")) {
			out[key] = strings.TrimSpace(v)
		}
	}
	return out
}

// readNpmrc parses an .npmrc as npm's ini reader does for the lines that
// matter here. That is key = value, comments starting with ; or #, quoted values, and
// ${VAR} replaced from the environment in keys and values.
func readNpmrc(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' || line[0] == '#' || line[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = expandNpmrcEnv(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = expandNpmrcEnv(v)
	}
	return out
}

// expandNpmrcEnv replaces ${NAME} and ${NAME?} with the variable's value. An
// unset variable becomes empty, so a token line naming one is ignored.
func expandNpmrcEnv(s string) string {
	var b strings.Builder
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		name := strings.TrimSuffix(s[start+2:start+end], "?")
		b.WriteString(os.Getenv(name))
		s = s[start+end+1:]
	}
}

// registryFor returns the registry URL npm fetches pkg from, ending in "/".
func (c npmRegistryConfig) registryFor(pkg string) string {
	reg := c.registry
	if strings.HasPrefix(pkg, "@") {
		if scope, _, ok := strings.Cut(pkg, "/"); ok {
			if r, ok := c.scopes[strings.ToLower(scope)]; ok && r != "" {
				reg = r
			}
		}
	}
	if reg == "" {
		reg = publicNpmRegistry
	}
	if !strings.HasSuffix(reg, "/") {
		reg += "/"
	}
	return reg
}

// tokenFor finds the _authToken npm would send to registry. That is the longest
// "//host/path/" key that the registry URL starts with, down to "//host/".
func (c npmRegistryConfig) tokenFor(registry string) string {
	u, err := url.Parse(registry)
	if err != nil || u.Host == "" {
		return ""
	}
	path := strings.TrimSuffix(u.Path, "/")
	for {
		if t := c.tokens["//"+u.Host+path+"/"]; t != "" {
			return t
		}
		if path == "" {
			return ""
		}
		path = path[:strings.LastIndex(path, "/")]
	}
}

// isPublicNpmRegistry reports whether a registry URL is the public npm
// registry, the only one the download counts and OSV describe.
func isPublicNpmRegistry(registry string) bool {
	u, err := url.Parse(registry)
	if err != nil {
		return false
	}
	pub, _ := url.Parse(publicNpmRegistry)
	return isNpmRegistryHost(u.Hostname()) || (pub != nil && strings.EqualFold(u.Host, pub.Host))
}

// registryHost is the host part of a registry URL, for messages.
func registryHost(registry string) string {
	if u, err := url.Parse(registry); err == nil && u.Host != "" {
		return u.Host
	}
	return registry
}

// checkRegistries is the configuration the metadata lookups in this process
// read. Set by runVerifyTargets before it fetches anything. The lookups sit
// behind seams whose signatures many tests replace, which is why this is not
// passed to them. nvx runs one set of checks per process.
var (
	checkRegistriesMu sync.RWMutex
	checkRegistries   npmRegistryConfig
)

func setCheckRegistries(c npmRegistryConfig) {
	checkRegistriesMu.Lock()
	checkRegistries = c
	checkRegistriesMu.Unlock()
}

func currentCheckRegistries() npmRegistryConfig {
	checkRegistriesMu.RLock()
	defer checkRegistriesMu.RUnlock()
	return checkRegistries
}
