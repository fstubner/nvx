package main

import "testing"

func TestResolveRuntimeSelector(t *testing.T) {
	cases := []struct {
		query       string
		wantRuntime string
		wantVersion string
	}{
		{"20", "node", "20"},           // bare version -> default runtime
		{"node@20", "node", "20"},      // explicit node
		{"bun@1.1", "bun", "1.1"},      // explicit bun
		{"bun", "bun", "latest"},       // lone runtime name -> latest
		{"node", "node", "latest"},     // lone default runtime name
		{"lts", "node", "lts"},         // node dist-tag
		{"18.16.0", "node", "18.16.0"}, // full version
	}
	for _, c := range cases {
		p, ver := ResolveRuntimeSelector(c.query)
		if p == nil {
			t.Fatalf("ResolveRuntimeSelector(%q) returned nil provider", c.query)
		}
		if p.Name() != c.wantRuntime || ver != c.wantVersion {
			t.Errorf("ResolveRuntimeSelector(%q) = (%s, %q), want (%s, %q)",
				c.query, p.Name(), ver, c.wantRuntime, c.wantVersion)
		}
	}
}

func TestRuntimeRegistry(t *testing.T) {
	names := RuntimeNames()
	if len(names) < 2 {
		t.Fatalf("expected at least node+bun registered, got %v", names)
	}
	for _, want := range []string{"node", "bun"} {
		if _, ok := Providers[want]; !ok {
			t.Errorf("runtime %q not registered", want)
		}
	}
}

func TestBunTagToVersion(t *testing.T) {
	cases := map[string]string{
		"bun-v1.1.30": "v1.1.30",
		"bun-1.1.30":  "v1.1.30",
		"v1.2.0":      "v1.2.0",
	}
	for tag, want := range cases {
		if got := bunTagToVersion(tag); got != want {
			t.Errorf("bunTagToVersion(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestBunShimsOwnedByBun(t *testing.T) {
	// The shim router must map bun/bunx to the bun runtime, not node.
	if got := runtimeForShim("bun").Name(); got != "bun" {
		t.Errorf("runtimeForShim(bun) = %s, want bun", got)
	}
	if got := runtimeForShim("bunx").Name(); got != "bun" {
		t.Errorf("runtimeForShim(bunx) = %s, want bun", got)
	}
	// And node keeps its ecosystem tools.
	if got := runtimeForShim("npm").Name(); got != "node" {
		t.Errorf("runtimeForShim(npm) = %s, want node", got)
	}
}

func TestIsolationProviderRegistry(t *testing.T) {
	for _, name := range []string{"native", "docker", "wsl", "wslc", "sandbox-exec", "seatbelt", "systemd-nspawn", "nspawn", "container"} {
		if _, ok := GetIsolationProvider(name); !ok {
			t.Errorf("isolation provider %q not registered", name)
		}
	}
	// native must always be available; the registry must expose canonical names.
	if p, _ := GetIsolationProvider("native"); !p.Available() {
		t.Error("native isolation provider should always be available")
	}
	names := IsolationProviderNames()
	if len(names) < 6 {
		t.Errorf("expected >=6 canonical isolation providers, got %v", names)
	}
}
