// Package hooks reports payload entries that change how the system trusts
// code. A dynamic linker preload, kernel module, device node, PAM config,
// system generator, sysctl snippet, or login profile is red. A user-manager
// generator is yellow, because it runs as the user who logs in.
// The finding records the mode and owner. An ordinary file abstains.
package hooks

import (
	"fmt"
	"path"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	modeType = 0170000
	modeFifo = 0010000
	modeChr  = 0020000
	modeBlk  = 0060000
	modeSock = 0140000
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "hooks" }

func (module) Analyze(files []check.File) check.Report {
	var issues []check.Issue
	for _, f := range files {
		if f.Kind == check.KindScriptlet || f.Kind == check.KindDir {
			continue
		}
		color, code, detail, ok := fileFinding(f)
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: code, Detail: detail, Color: color})
	}
	return check.Report{Issues: issues}
}

func fileFinding(f check.File) (string, string, string, bool) {
	detail := entryDetail(f)
	switch f.Mode & modeType {
	case modeChr, modeBlk:
		return check.ColorRed, "hooks.device", detail, true
	case modeFifo, modeSock:
		return check.ColorRed, "hooks.special", detail, true
	}
	color, code, scope, ok := hookPath(f.Path)
	if !ok {
		return "", "", "", false
	}
	if scope != "" {
		detail = "scope=" + scope + " " + detail
	}
	return color, code, detail, true
}

func entryDetail(f check.File) string {
	if f.Kind == check.KindSymlink {
		return "symlink"
	}
	return fmt.Sprintf("mode %#o user %s group %s", f.Mode, f.User, f.Group)
}

func hookPath(name string) (string, string, string, bool) {
	switch {
	case name == "/etc/ld.so.preload" || name == "/etc/ld.so.conf" || strings.HasPrefix(name, "/etc/ld.so.conf.d/"):
		return check.ColorRed, "hooks.linker", "", true
	case strings.HasPrefix(name, "/lib/modules/") || strings.HasPrefix(name, "/usr/lib/modules/"):
		return check.ColorRed, "hooks.module", "", true
	}
	if kernelModule(name) {
		return check.ColorRed, "hooks.module", "", true
	}
	switch path.Dir(name) {
	case "/lib/security", "/lib64/security", "/usr/lib/security", "/usr/lib64/security":
		return check.ColorRed, "hooks.pam", "", true
	case "/usr/lib/systemd/system-generators", "/lib/systemd/system-generators", "/etc/systemd/system-generators":
		return check.ColorRed, "hooks.generator", "system", true
	case "/usr/lib/systemd/user-generators", "/lib/systemd/user-generators", "/etc/systemd/user-generators":
		return check.ColorYellow, "hooks.generator", "user", true
	}
	if strings.HasPrefix(name, "/etc/pam.d/") {
		return check.ColorRed, "hooks.pam", "", true
	}
	if bootConfig(name) {
		return check.ColorRed, "hooks.boot", "", true
	}
	if profile(name) {
		return check.ColorRed, "hooks.profile", "", true
	}
	return "", "", "", false
}

func kernelModule(name string) bool {
	base := path.Base(name)
	for _, suffix := range []string{".ko", ".ko.gz", ".ko.xz", ".ko.zst"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func bootConfig(name string) bool {
	switch name {
	case "/etc/sysctl.conf", "/etc/modules":
		return true
	}
	switch path.Dir(name) {
	case "/etc/sysctl.d", "/usr/lib/sysctl.d", "/lib/sysctl.d",
		"/etc/modprobe.d", "/usr/lib/modprobe.d", "/lib/modprobe.d",
		"/etc/modules-load.d", "/usr/lib/modules-load.d", "/lib/modules-load.d":
		return true
	default:
		return false
	}
}

func profile(name string) bool {
	switch name {
	case "/etc/profile", "/etc/bashrc", "/etc/bash.bashrc", "/etc/zshrc":
		return true
	}
	switch path.Dir(name) {
	case "/etc/profile.d", "/usr/lib/environment.d", "/etc/environment.d":
		return true
	default:
		return false
	}
}
