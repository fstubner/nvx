//go:build windows

package nvx

// Evidence helpers for the Windows .env probes (NVX_PROBE=1). See
// TestWindowsDotenvProtectionExperiments for what was measured and why.

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// tokenInfo reads one token information class for the current process.
func tokenInfo(class uintptr) ([]byte, error) {
	var tok syscall.Token
	proc, _, _ := procGetCurrentProcess.Call()
	if r, _, e := procOpenProcessToken.Call(proc, uintptr(TOKEN_QUERY), uintptr(unsafe.Pointer(&tok))); r == 0 {
		return nil, fmt.Errorf("OpenProcessToken: %v", e)
	}
	defer syscall.CloseHandle(syscall.Handle(tok))
	var need uint32
	procGetTokenInformation.Call(uintptr(tok), class, 0, 0, uintptr(unsafe.Pointer(&need)))
	if need == 0 {
		return nil, fmt.Errorf("class %d: no size", class)
	}
	buf := make([]byte, need)
	if r, _, e := procGetTokenInformation.Call(uintptr(tok), class, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(need), uintptr(unsafe.Pointer(&need))); r == 0 {
		return nil, fmt.Errorf("GetTokenInformation(%d): %v", class, e)
	}
	return buf, nil
}

// sidAttrString decodes the SID_AND_ATTRIBUTES at p.
func sidAttrString(p unsafe.Pointer) string {
	sid := *(**byte)(p)
	attr := *(*uint32)(unsafe.Add(p, 8))
	s, err := sidToString(sid)
	if err != nil {
		return "?"
	}
	return fmt.Sprintf("%s/0x%x", s, attr)
}

func tokenGroupList(class uintptr) []string {
	buf, err := tokenInfo(class)
	if err != nil {
		return []string{"ERR:" + err.Error()}
	}
	n := *(*uint32)(unsafe.Pointer(&buf[0]))
	var out []string
	for i := uint32(0); i < n; i++ {
		out = append(out, sidAttrString(unsafe.Pointer(&buf[8+uintptr(i)*16])))
	}
	return out
}

// reportOwnToken prints what the calling process's token is, one KEY=value line
// each, so the parent can see whether the process doing a read is the sandboxed one.
func reportOwnToken() {
	u32 := func(class uintptr) string {
		b, err := tokenInfo(class)
		if err != nil {
			return "ERR:" + err.Error()
		}
		return fmt.Sprint(*(*uint32)(unsafe.Pointer(&b[0])))
	}
	fmt.Printf("TOKEN_APPCONTAINER=%s\n", u32(29))
	if b, err := tokenInfo(25); err == nil {
		fmt.Printf("TOKEN_IL=%s\n", sidAttrString(unsafe.Pointer(&b[0])))
	}
	fmt.Printf("TOKEN_MANDATORY_POLICY=%s\n", u32(27))
	if b, err := tokenInfo(1); err == nil {
		fmt.Printf("TOKEN_USER=%s\n", sidAttrString(unsafe.Pointer(&b[0])))
	}
	if b, err := tokenInfo(4); err == nil {
		s, _ := sidToString(*(**byte)(unsafe.Pointer(&b[0])))
		fmt.Printf("TOKEN_OWNER=%s\n", s)
	}
	if b, err := tokenInfo(31); err == nil {
		s, _ := sidToString(*(**byte)(unsafe.Pointer(&b[0])))
		fmt.Printf("TOKEN_PKGSID=%s\n", s)
	}
	fmt.Printf("TOKEN_CAPS=%s\n", strings.Join(tokenGroupList(30), ","))
	for _, g := range tokenGroupList(2) {
		fmt.Printf("TOKEN_GROUP=%s\n", g)
	}
}

// childTamper is what the contained child tries against the secret after reading
// it: append, rename away, delete. Printed as TAMPER_x=OK|DENIED.
func childTamper(secret string) {
	res := func(name string, err error) {
		if err != nil {
			fmt.Printf("TAMPER_%s=DENIED\n", name)
		} else {
			fmt.Printf("TAMPER_%s=OK\n", name)
		}
	}
	f, err := os.OpenFile(secret, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		f.Close()
	}
	res("WRITE", err)
	res("RENAME", os.Rename(secret, secret+".moved"))
	res("DELETE", os.Remove(secret))
}

// childExtra reads NVX_PROBE_EXTRA ("label=path|label=path") and reports each.
func childExtra() {
	for _, spec := range strings.Split(os.Getenv("NVX_PROBE_EXTRA"), "|") {
		p := strings.SplitN(spec, "=", 2)
		if len(p) != 2 {
			continue
		}
		if b, err := os.ReadFile(p[1]); err != nil {
			fmt.Printf("%s=DENIED\n", p[0])
		} else {
			fmt.Printf("%s=READ:%s\n", p[0], strings.TrimSpace(string(b)))
		}
	}
}

// childAppend opens each "label=path" in NVX_PROBE_APPEND for writing.
func childAppend() {
	for _, spec := range strings.Split(os.Getenv("NVX_PROBE_APPEND"), "|") {
		p := strings.SplitN(spec, "=", 2)
		if len(p) != 2 {
			continue
		}
		f, err := os.OpenFile(p[1], os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			fmt.Printf("%s=DENIED\n", p[0])
			continue
		}
		f.Close()
		fmt.Printf("%s=OK\n", p[0])
	}
}

// sddlOf returns owner, DACL and label of path as SDDL.
func sddlOf(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "ERR:" + err.Error()
	}
	const info = 0x1 | 0x4 | 0x10 // owner, DACL, label
	var sd *byte
	if rc, _, _ := procGetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(p)), seFileObject, info,
		0, 0, 0, 0, uintptr(unsafe.Pointer(&sd))); rc != 0 {
		return fmt.Sprintf("ERR:GetNamedSecurityInfo %d", rc)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	var out *uint16
	if r, _, e := procConvertSDToStringSD.Call(uintptr(unsafe.Pointer(sd)), 1, info,
		uintptr(unsafe.Pointer(&out)), 0); r == 0 {
		return "ERR:" + e.Error()
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(out)))
	return syscall.UTF16ToString(unsafe.Slice(out, 8192))
}

// logProbeEvidence logs the file's SDDL and the child's own token report.
func logProbeEvidence(t *testing.T, secret, childOutput string) {
	t.Helper()
	t.Logf("SDDL of .env as the child saw it:\n%s", sddlOf(secret))
	var groups []string
	for _, line := range strings.Split(childOutput, "\n") {
		switch {
		case strings.HasPrefix(line, "TOKEN_GROUP="):
			groups = append(groups, strings.TrimPrefix(line, "TOKEN_GROUP="))
		case strings.HasPrefix(line, "TOKEN_"):
			t.Logf("child %s", line)
		}
	}
	t.Logf("child TOKEN_GROUPS=%s", strings.Join(groups, " "))
}
