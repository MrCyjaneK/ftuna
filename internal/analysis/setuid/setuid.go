// Package setuid reports payload entries whose header mode has the setuid or setgid bit.
// A setuid root binary is red. A setuid binary for another user is red.
// A setuid directory, a setuid script, or a world-writable privileged file is red.
// A setgid directory only changes the group of new files: red or wheel is yellow,
// and any other group is green. Other setgid entries are red.
// Symlinks are skipped: the mode does not describe the target.
// A package with neither bit abstains.
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
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "setuid" }

func (module) Analyze(files []check.File) check.Report {
	var out []check.Issue
	for _, f := range files {
		if f.Kind == check.KindScriptlet || f.Kind == check.KindSymlink {
			continue
		}
		out = append(out, fileFindings(f)...)
	}
	return check.Report{Issues: out}
}

func fileFindings(f check.File) []check.Issue {
	kind := entryKind(f)
	detail := fmt.Sprintf("%s mode %#o user %s group %s %s", f.Path, f.Mode, f.User, f.Group, kind)
	if f.Mode&otherWrite != 0 {
		detail += " world-writable"
	}
	var out []check.Issue
	if f.Mode&setuidBit != 0 {
		out = append(out, check.Issue{Path: f.Path, Code: "setuid", Detail: detail, Color: check.ColorRed})
	}
	if f.Mode&setgidBit != 0 {
		color := check.ColorRed
		if kind == "dir" && !privileged(f.Group, "root", "wheel") && f.Mode&otherWrite == 0 {
			color = check.ColorGreen
		} else if kind == "dir" && f.Mode&otherWrite == 0 {
			color = check.ColorYellow
		}
		out = append(out, check.Issue{Path: f.Path, Code: "setgid", Detail: detail, Color: color})
	}
	return out
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
