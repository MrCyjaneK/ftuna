// Package worldwrite scores files and directories that other users can write.
// Every such entry scores 0. The finding still says whether it is a
// directory, a sticky directory, an executable, or a plain file.
// Symlinks are skipped: their mode does not describe the target.
// The module score is 0 when any entry matches. A package with none abstains.
package worldwrite

import (
	"fmt"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	otherWrite = 0002
	sticky     = 01000
	execute    = 0111
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "worldwrite" }

func (module) Analyze(files []check.File) check.Report {
	score := check.Neutral
	var issues []check.Issue
	for _, f := range files {
		if f.Kind == check.KindSymlink || f.Kind == check.KindScriptlet || f.Mode&otherWrite == 0 {
			continue
		}
		issues = append(issues, check.Issue{
			Path:   f.Path,
			Code:   "worldwrite",
			Detail: fmt.Sprintf("%s mode %#o user %s group %s %s", f.Path, f.Mode, f.User, f.Group, kindOf(f)),
		})
		score = 0
	}
	return check.Report{Score: score, Issues: issues}
}

func kindOf(f check.File) string {
	if f.Kind == check.KindDir {
		if f.Mode&sticky != 0 {
			return "dir sticky"
		}
		return "dir"
	}
	if f.Mode&execute != 0 {
		return "exec"
	}
	return "file"
}
