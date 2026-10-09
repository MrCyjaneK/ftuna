// Package deps chooses install dependencies that this tree itself publishes.
package deps

import (
	"fmt"
	"sort"
	"strings"
)

type RPM struct {
	File     string
	Name     string
	Version  string
	Release  string
	Summary  string
	Provides []string
	Requires []string
	SHA256   string
}

// Capability is the name token of an RPM require or provide.
func Capability(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.HasPrefix(spec, "(") {
		return ""
	}
	return strings.Fields(spec)[0]
}

// Select returns published RPMs that the built set needs so a one-off repo
// can install. A require that names one of our packages but has no RPM for
// this architecture is an error. Requires we do not publish are skipped.
func Select(built, published []RPM, ourNames map[string]struct{}) ([]RPM, error) {
	lookup := map[string][]RPM{}
	add := func(r RPM) {
		for _, name := range append([]string{r.Name}, r.Provides...) {
			if name == "" {
				continue
			}
			lookup[name] = append(lookup[name], r)
		}
	}
	for _, r := range built {
		add(r)
	}
	for _, r := range published {
		add(r)
	}

	seen := map[string]bool{}
	for _, r := range built {
		seen[r.File] = true
	}
	queue := append([]RPM{}, built...)
	var extra []RPM
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, req := range cur.Requires {
			cap := Capability(req)
			if cap == "" {
				continue
			}
			if _, ours := ourNames[cap]; !ours {
				continue
			}
			matches := lookup[cap]
			if len(matches) == 0 {
				return nil, fmt.Errorf("missing our package %s for this architecture (required by %s)", cap, cur.Name)
			}
			for _, m := range matches {
				if seen[m.File] {
					continue
				}
				seen[m.File] = true
				extra = append(extra, m)
				queue = append(queue, m)
			}
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].File < extra[j].File })
	return extra, nil
}
