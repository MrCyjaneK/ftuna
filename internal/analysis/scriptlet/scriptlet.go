// Package scriptlet scores RPM scriptlets. They run as root during install,
// erase, or verification. A script with commands scores 30. A header that
// only names a known helper, such as ldconfig, scores 80: it is still root,
// and it is not the package's own code. An empty shell script abstains.
// The finding records the interpreter and the first commands. The module
// score is the worst scriptlet.
package scriptlet

import (
	"path"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	scoreRoot   = 30
	scoreHelper = 80
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "scriptlet" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		points, detail, ok := fileScore(f)
		if !ok {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: "scriptlet", Detail: detail})
		score = check.Worse(score, points)
	}
	return check.Report{Score: score, Issues: issues}
}

func fileScore(f check.File) (int, string, bool) {
	if f.Kind != check.KindScriptlet {
		return 0, "", false
	}
	prog := strings.TrimSpace(f.Program)
	if prog == "" {
		prog = "/bin/sh"
	}
	lines := commands(f.Body)
	if len(lines) == 0 {
		switch {
		case knownHelper(prog):
			return scoreHelper, "user=root program=" + prog, true
		case shellProgram(prog):
			return 0, "", false
		default:
			return scoreRoot, "user=root program=" + prog, true
		}
	}
	return scoreRoot, "user=root program=" + prog + " body=" + preview(lines), true
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
