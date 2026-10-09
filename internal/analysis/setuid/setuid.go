// Package setuid reports payload entries whose header mode has the setuid or setgid bit.
// A setuid root binary scores 0. A setuid binary for another user scores 20.
// Setgid follows the same split, higher because the extra group is narrower.
// A setuid directory, a setuid script, or a world-writable privileged file is
// confusing and scores 0. A setgid directory only changes the group of new
// files, so it scores 55 for root or wheel and 80 otherwise.
// Symlinks are skipped: the mode does not describe the target.
// The module score is the worst entry. A package with neither bit abstains.
package setuid

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

const (
	setuidBit  = 04000
	setgidBit  = 02000
	otherWrite = 0002

	scoreSetuidRoot    = 0
	scoreSetuidUser    = 20
	scoreSetgidRoot    = 25
	scoreSetgidUser    = 45
	scoreSetgidDir     = 55
	scoreSetgidDirUser = 80
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "setuid" }

func (module) Analyze(files []check.File) check.Report {
	worst := check.Neutral
	var out []check.Issue
	for _, f := range files {
		if f.Kind == check.KindScriptlet || f.Kind == check.KindSymlink {
			continue
		}
		found, score, ok := fileScore(f)
		if !ok {
			continue
		}
		out = append(out, found...)
		if worst < 0 || score < worst {
			worst = score
		}
	}
	return check.Report{Score: worst, Issues: out}
}

func fileScore(f check.File) ([]check.Issue, int, bool) {
	kind := entryKind(f)
	detail := fmt.Sprintf("%s mode %#o user %s group %s %s", f.Path, f.Mode, f.User, f.Group, kind)
	if f.Mode&otherWrite != 0 {
		detail += " world-writable"
	}
	worst := check.Neutral
	var out []check.Issue
	if f.Mode&setuidBit != 0 {
		out = append(out, check.Issue{Path: f.Path, Code: "setuid", Detail: detail})
		score := scoreSetuidUser
		if privileged(f.User, "root") || kind == "script" || kind == "dir" || f.Mode&otherWrite != 0 {
			score = scoreSetuidRoot
		}
		worst = score
	}
	if f.Mode&setgidBit != 0 {
		out = append(out, check.Issue{Path: f.Path, Code: "setgid", Detail: detail})
		score := scoreSetgidUser
		switch {
		case kind == "script" || f.Mode&otherWrite != 0:
			score = scoreSetuidRoot
		case kind == "dir" && privileged(f.Group, "root", "wheel"):
			score = scoreSetgidDir
		case kind == "dir":
			score = scoreSetgidDirUser
		case privileged(f.Group, "root", "wheel"):
			score = scoreSetgidRoot
		}
		if worst < 0 || score < worst {
			worst = score
		}
	}
	return out, worst, len(out) > 0
}

func entryKind(f check.File) string {
	switch f.Kind {
	case check.KindDir:
		return "dir"
	case check.KindGhost:
		return "ghost"
	}
	if bytes.HasPrefix(bytes.TrimSpace(f.Body), []byte("#!")) {
		return "script"
	}
	return "file"
}

func privileged(name string, groups ...string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "0":
		return true
	}
	for _, group := range groups {
		if strings.EqualFold(name, group) {
			return true
		}
	}
	return false
}
