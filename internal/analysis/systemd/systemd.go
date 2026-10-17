// Package systemd reports service, timer, socket, and path units shipped in
// a package. A unit is red, yellow, or green from its scope, the user and
// group it runs as, and the sandbox settings that are actually turned on.
// Socket, path, and timer units are judged on their own.
package systemd

import (
	"path"
	"sort"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

func init() { check.Register(module{}) }

type module struct{}

func (module) Name() string { return "systemd" }

func (module) Analyze(files []check.File) check.Report {
	units := collect(files)
	if len(units) == 0 {
		return check.Report{}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].path < units[j].path })
	var issues []check.Issue
	for _, unit := range units {
		issues = append(issues, check.Issue{
			Path:   unit.path,
			Code:   unit.code,
			Detail: unit.detail,
			Color:  unit.color,
		})
	}
	return check.Report{Issues: issues}
}

type found struct {
	path   string
	code   string
	detail string
	color  string
}

type identity struct {
	scope  string
	name   string
	dropin bool
}

type fragment struct {
	path   string
	body   []byte
	kind   string
	dropin bool
	id     identity
}

type bundle struct {
	main  *fragment
	link  *fragment
	drops []fragment
}

func collect(files []check.File) []found {
	grouped := map[string]*bundle{}
	var order []string
	for i := range files {
		frag, ok := fragmentFrom(files[i])
		if !ok {
			continue
		}
		key := frag.id.scope + "/" + frag.id.name
		b := grouped[key]
		if b == nil {
			b = &bundle{}
			grouped[key] = b
			order = append(order, key)
		}
		switch {
		case frag.dropin && frag.kind == check.KindFile:
			b.drops = append(b.drops, frag)
		case frag.kind == check.KindFile && b.main == nil:
			cp := frag
			b.main = &cp
		case frag.kind == check.KindSymlink && b.link == nil:
			cp := frag
			b.link = &cp
		}
	}
	var out []found
	for _, key := range order {
		if unit, ok := grouped[key].judge(); ok {
			out = append(out, unit)
		}
	}
	return out
}

func fragmentFrom(f check.File) (fragment, bool) {
	if f.Kind != check.KindFile && f.Kind != check.KindSymlink {
		return fragment{}, false
	}
	id, ok := identify(f.Path)
	if !ok {
		return fragment{}, false
	}
	return fragment{path: f.Path, body: f.Body, kind: f.Kind, dropin: id.dropin, id: id}, true
}

func identify(name string) (identity, bool) {
	dir := path.Dir(name)
	base := path.Base(name)
	id := identity{}
	if strings.HasSuffix(dir, ".d") {
		unit := strings.TrimSuffix(path.Base(dir), ".d")
		if !isUnit(unit) {
			return identity{}, false
		}
		dir = path.Dir(dir)
		id.name = unit
		id.dropin = true
	} else if isUnit(base) {
		id.name = base
	} else {
		return identity{}, false
	}
	switch path.Base(dir) {
	case "wants", "requires", "upholds":
		return identity{}, false
	}
	switch dir {
	case "/usr/lib/systemd/user", "/lib/systemd/user", "/etc/systemd/user":
		id.scope = "user"
	case "/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system":
		id.scope = "system"
	default:
		return identity{}, false
	}
	return id, true
}

func isUnit(name string) bool {
	return strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer") ||
		strings.HasSuffix(name, ".socket") || strings.HasSuffix(name, ".path")
}

func (b *bundle) judge() (found, bool) {
	if b.main == nil && len(b.drops) == 0 && b.link == nil {
		return found{}, false
	}
	if b.main == nil && len(b.drops) == 0 {
		return b.linkOnly(), true
	}
	text := b.merged()
	id := b.identity()
	unitPath := b.issuePath()
	if code, ok := activationCode(id.name); ok {
		target := activationTarget(id.name, parseUnit(text))
		return judgeActivation(id, unitPath, code, target), true
	}
	return judgeService(id, unitPath, text, true), true
}

func (b *bundle) identity() identity {
	switch {
	case b.main != nil:
		return b.main.id
	case len(b.drops) > 0:
		return b.drops[0].id
	default:
		return b.link.id
	}
}

func (b *bundle) issuePath() string {
	if b.main != nil {
		return b.main.path
	}
	if len(b.drops) > 0 {
		drops := append([]fragment(nil), b.drops...)
		sort.Slice(drops, func(i, j int) bool { return drops[i].path < drops[j].path })
		return drops[0].path
	}
	return b.link.path
}

func (b *bundle) merged() []byte {
	var parts [][]byte
	if b.main != nil {
		parts = append(parts, b.main.body)
	}
	drops := append([]fragment(nil), b.drops...)
	sort.Slice(drops, func(i, j int) bool { return drops[i].path < drops[j].path })
	for _, drop := range drops {
		parts = append(parts, drop.body)
	}
	return joinLines(parts)
}

func (b *bundle) linkOnly() found {
	id := b.link.id
	if code, ok := activationCode(id.name); ok {
		target := activationTarget(id.name, parseUnit(nil))
		return judgeActivation(id, b.link.path, code, target)
	}
	return judgeService(id, b.link.path, nil, false)
}

func joinLines(parts [][]byte) []byte {
	n := 0
	for _, part := range parts {
		n += len(part) + 1
	}
	out := make([]byte, 0, n)
	for _, part := range parts {
		out = append(out, part...)
		out = append(out, '\n')
	}
	return out
}
