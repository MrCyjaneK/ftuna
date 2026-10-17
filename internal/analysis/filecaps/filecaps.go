// Package filecaps reports Linux file capabilities from the RPM header.
// A file capability is red: it can change credentials, bypass file
// permissions, or load kernel code, and a narrower or unknown name is
// still hard to account for. The finding records the capability text and,
// when the header has them, the mode and owner.
// A package with no file capabilities abstains.
package filecaps

import (
	"fmt"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "filecaps" }

func (module) Analyze(files []check.File) check.Report {
	var issues []check.Issue
	for _, f := range files {
		if f.Kind == check.KindScriptlet || strings.TrimSpace(f.Caps) == "" {
			continue
		}
		issues = append(issues, check.Issue{Path: f.Path, Code: "filecaps", Detail: capsDetail(f), Color: check.ColorRed})
	}
	return check.Report{Issues: issues}
}

func capsDetail(f check.File) string {
	detail := strings.TrimSpace(f.Caps)
	if f.Mode == 0 && f.User == "" && f.Group == "" {
		return detail
	}
	return fmt.Sprintf("%s mode %#o user %s group %s", detail, f.Mode, f.User, f.Group)
}
