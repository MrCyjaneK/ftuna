// Package sailjail reads [X-Sailjail] from desktop files installed in
// /usr/share/applications. An application with no sandbox section is a
// user process and scores 75. Sandboxing=Disabled is an explicit opt-out
// and scores 55. A sandboxed application scores 100. Permissions are
// listed and do not change that score. The module score is the worst
// desktop file. A package with no application desktop abstains.
package sailjail

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	scoreAbsent   = 75
	scoreDisabled = 55
	scoreJailed   = 100
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "sailjail" }

func (module) Analyze(files []check.File) check.Report {
	worst := check.Neutral
	var out []check.Issue
	for _, f := range files {
		if f.Kind != check.KindFile || !isAppDesktop(f.Path) {
			continue
		}
		issues, score := desktop(f)
		out = append(out, issues...)
		if worst < 0 || score < worst {
			worst = score
		}
	}
	return check.Report{Score: worst, Issues: out}
}

func desktop(f check.File) ([]check.Issue, int) {
	hasGroup, disabled, perms := parseDesktop(f.Body)
	if !hasGroup {
		return []check.Issue{{
			Path:   f.Path,
			Code:   "sailjail.absent",
			Detail: "no [X-Sailjail] section",
		}}, scoreAbsent
	}
	var out []check.Issue
	if disabled {
		out = append(out, check.Issue{
			Path:   f.Path,
			Code:   "sailjail.disabled",
			Detail: "Sandboxing=Disabled",
		})
	}
	for _, perm := range perms {
		out = append(out, check.Issue{
			Path:   f.Path,
			Code:   "sailjail.permission",
			Detail: perm,
		})
	}
	if disabled {
		return out, scoreDisabled
	}
	return out, scoreJailed
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
