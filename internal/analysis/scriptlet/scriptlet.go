// Package scriptlet reports RPM scriptlets. They run as root during install,
// erase, or verification. A script with commands is red. A header that
// only names a known helper, such as ldconfig, is green: it is still root,
// and it is not the package's own code. An empty shell script abstains.
// The finding records the interpreter and the first commands.
package scriptlet

import (
	"path"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "scriptlet" }

func (module) Analyze(files []check.File) check.Report {
	var issues []check.Issue
	for _, f := range files {
		color, detail, ok := fileFinding(f)
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: "scriptlet", Detail: detail, Color: color})
	}
	return check.Report{Issues: issues}
}

func fileFinding(f check.File) (string, string, bool) {
	if f.Kind != check.KindScriptlet {
		return "", "", false
	}
	prog := strings.TrimSpace(f.Program)
	if prog == "" {
		prog = "/bin/sh"
	}
	lines := commands(f.Body)
	if len(lines) == 0 {
		switch {
		case knownHelper(prog):
			return check.ColorGreen, "user=root program=" + prog, true
		case shellProgram(prog):
			return "", "", false
		default:
			return check.ColorRed, "user=root program=" + prog, true
		}
	}
	return check.ColorRed, "user=root program=" + prog + " body=" + preview(lines), true
}

func commands(body []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func preview(lines []string) string {
	if len(lines) > 3 {
		lines = lines[:3]
	}
	text := strings.Join(lines, "; ")
	if len(text) > 160 {
		text = text[:160]
	}
	return text
}

func knownHelper(prog string) bool {
	switch path.Base(prog) {
	case "ldconfig", "systemd-sysusers", "systemd-tmpfiles", "systemd-hwdb",
		"glib-compile-schemas", "update-desktop-database", "gtk-update-icon-cache",
		"gio-querymodules":
		return true
	default:
		return false
	}
}

func shellProgram(prog string) bool {
	switch path.Base(prog) {
	case "sh", "bash", "dash":
		return true
	default:
		return false
	}
}
