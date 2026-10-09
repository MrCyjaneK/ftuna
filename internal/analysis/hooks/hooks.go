// Package hooks scores payload entries that change how the system trusts
// code. A dynamic linker preload scores 5. A kernel module or device node
// scores 10. PAM scores 15. A system generator or sysctl snippet scores 20.
// A login profile scores 40, because it runs as the user who logs in. A
// user-manager generator scores 75. The finding records the mode and owner.
// The module score is the worst entry. An ordinary file abstains.
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

	scoreLinker  = 5
	scoreModule  = 10
	scoreDevice  = 10
	scorePAM     = 15
	scoreBoot    = 20
	scoreSpecial = 35
	scoreProfile = 40
	scoreUserGen = 75
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "hooks" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		if f.Kind == check.KindScriptlet || f.Kind == check.KindDir {
			continue
		}
		points, code, detail, ok := fileScore(f)
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: code, Detail: detail})
		score = check.Worse(score, points)
	}
	return check.Report{Score: score, Issues: issues}
}

func fileScore(f check.File) (int, string, string, bool) {
	detail := entryDetail(f)
	switch f.Mode & modeType {
	case modeChr, modeBlk:
		return scoreDevice, "hooks.device", detail, true
	case modeFifo, modeSock:
		return scoreSpecial, "hooks.special", detail, true
	}
	points, code, scope, ok := hookPath(f.Path)
	if !ok {
		return 0, "", "", false
	}
	if scope != "" {
		detail = "scope=" + scope + " " + detail
	}
	return points, code, detail, true
}

func entryDetail(f check.File) string {
	if f.Kind == check.KindSymlink {
		return "symlink"
	}
	return fmt.Sprintf("mode %#o user %s group %s", f.Mode, f.User, f.Group)
}

func hookPath(name string) (int, string, string, bool) {
	switch {
	case name == "/etc/ld.so.preload" || name == "/etc/ld.so.conf" || strings.HasPrefix(name, "/etc/ld.so.conf.d/"):
		return scoreLinker, "hooks.linker", "", true
	case strings.HasPrefix(name, "/lib/modules/") || strings.HasPrefix(name, "/usr/lib/modules/"):
		return scoreModule, "hooks.module", "", true
	}
	if kernelModule(name) {
		return scoreModule, "hooks.module", "", true
	}
	switch path.Dir(name) {
	case "/lib/security", "/lib64/security", "/usr/lib/security", "/usr/lib64/security":
		return scorePAM, "hooks.pam", "", true
	case "/usr/lib/systemd/system-generators", "/lib/systemd/system-generators", "/etc/systemd/system-generators":
		return scoreBoot, "hooks.generator", "system", true
	case "/usr/lib/systemd/user-generators", "/lib/systemd/user-generators", "/etc/systemd/user-generators":
		return scoreUserGen, "hooks.generator", "user", true
	}
	if strings.HasPrefix(name, "/etc/pam.d/") {
		return scorePAM, "hooks.pam", "", true
	}
	if bootConfig(name) {
		return scoreBoot, "hooks.boot", "", true
	}
	if profile(name) {
		return scoreProfile, "hooks.profile", "", true
	}
	return 0, "", "", false
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
