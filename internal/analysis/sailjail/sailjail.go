// Package sailjail reads [X-Sailjail] from desktop files installed in
// /usr/share/applications. An application with no sandbox section is yellow.
// Sandboxing=Disabled is yellow. A sandboxed application is green.
// Permissions are listed in gray and do not change that color.
// A package with no application desktop abstains.
package sailjail

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "sailjail" }

func (module) Analyze(files []check.File) check.Report {
	var out []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile || !isAppDesktop(f.Path) {
			continue
		}
		out = append(out, desktop(f)...)
	}
	return check.Report{Issues: out}
}

func desktop(f check.File) []check.Issue {
	hasGroup, disabled, perms := parseDesktop(f.Body)
	if !hasGroup {
		return []check.Issue{{
			Path:   f.Path,
			Code:   "sailjail.absent",
			Detail: "no [X-Sailjail] section",
			Color:  check.ColorYellow,
		}}
	}
	var out []check.Issue
	color := check.ColorGreen
	if disabled {
		color = check.ColorYellow
		out = append(out, check.Issue{
			Path:   f.Path,
			Code:   "sailjail.disabled",
			Detail: "Sandboxing=Disabled",
			Color:  color,
		})
	} else if len(perms) == 0 {
		out = append(out, check.Issue{
			Path:   f.Path,
			Code:   "sailjail.jailed",
			Detail: "sandboxed",
			Color:  check.ColorGreen,
		})
	}
	for _, perm := range perms {
		out = append(out, check.Issue{
			Path:   f.Path,
			Code:   "sailjail.permission",
			Detail: perm,
			Color:  check.ColorGray,
		})
	}
	return out
}

func isAppDesktop(path string) bool {
	const dir = "/usr/share/applications/"
	if !strings.HasPrefix(path, dir) {
		return false
	}
	base := strings.TrimPrefix(path, dir)
	return base != "" && !strings.Contains(base, "/") && strings.HasSuffix(base, ".desktop")
}

func parseDesktop(body []byte) (hasGroup, disabled bool, perms []string) {
	group := ""
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			group = line[1 : len(line)-1]
			if group == "X-Sailjail" {
				hasGroup = true
			}
			continue
		}
		if group != "X-Sailjail" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "Sandboxing":
			if strings.EqualFold(val, "Disabled") {
				disabled = true
			}
		case "Permissions":
			for _, part := range strings.Split(val, ";") {
				part = strings.TrimSpace(part)
				if part != "" {
					perms = append(perms, part)
				}
			}
		}
	}
	return hasGroup, disabled, perms
}
