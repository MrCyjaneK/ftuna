// Package systemd scores service, timer, socket, and path units shipped in
// a package. The module score is the least contained unit. A unit file by
// itself is not a penalty: the score follows the scope, the user and group
// it runs as, and the sandbox settings that are actually turned on.
// Socket, path, and timer units use one scale. When the unit they activate
// is in the package, that service's score is the one that counts.
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
		return check.Report{Score: check.Neutral}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].path < units[j].path })
	worst := check.Neutral
	var issues []check.Issue
	for _, unit := range units {
		scored := unit.scored
		issues = append(issues, check.Issue{
			Path:   unit.path,
			Code:   unit.code,
			Detail: unit.detail,
		})
		if !scored {
			continue
		}
		if worst < 0 || unit.score < worst {
			worst = unit.score
		}
	}
	return check.Report{Score: worst, Issues: issues}
}

type found struct {
	path   string
	code   string
	detail string
	score  int
	scored bool
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
	known := map[string]bool{}
	for key, b := range grouped {
		if strings.HasSuffix(b.identity().name, ".service") {
			known[key] = true
		}
	}
	var out []found
	for _, key := range order {
		if unit, ok := grouped[key].judge(known); ok {
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

func (b *bundle) judge(known map[string]bool) (found, bool) {
	if b.main == nil && len(b.drops) == 0 && b.link == nil {
		return found{}, false
	}
	if b.main == nil && len(b.drops) == 0 {
		return b.linkOnly(known), true
	}
	text := b.merged()
	id := b.identity()
	unitPath := b.issuePath()
	if code, ok := activationCode(id.name); ok {
		target := activationTarget(id.name, parseUnit(text))
		_, seen := known[id.scope+"/"+target]
		return judgeActivation(id, unitPath, code, target, seen), true
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

func (b *bundle) linkOnly(known map[string]bool) found {
	id := b.link.id
	if code, ok := activationCode(id.name); ok {
		target := activationTarget(id.name, parseUnit(nil))
		_, seen := known[id.scope+"/"+target]
		return judgeActivation(id, b.link.path, code, target, seen)
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
