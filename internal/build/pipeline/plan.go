package pipeline

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
)

const (
	PublicRoot = "repo/ftuna-oss/"
	PublicKey  = "repo/ftuna-oss/ftuna-oss.key"
)

func ArchPrefix(version, arch string) string {
	return PublicRoot + version + "/" + arch + "/"
}

func ManifestKey(version string) string {
	return PublicRoot + version + "/manifest.json"
}

type RPMRec struct {
	File     string   `json:"file"`
	Name     string   `json:"name"`
	Version  string   `json:"version,omitempty"`
	Release  string   `json:"release,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Provides []string `json:"provides,omitempty"`
	Requires []string `json:"requires,omitempty"`
}

type ArchBuild struct {
	RPMs []RPMRec `json:"rpms"`
}

type RecipeBuild struct {
	Commit string               `json:"commit"`
	Arches map[string]ArchBuild `json:"arches"`
}

type Manifest struct {
	Recipes map[string]RecipeBuild `json:"recipes"`
}

func emptyManifest() Manifest {
	return Manifest{Recipes: map[string]RecipeBuild{}}
}

func NeedsBuild(r recipe.Recipe, man Manifest, version string, force bool, exists func(string) (bool, error)) (bool, error) {
	if force {
		return true, nil
	}
	rb, ok := man.Recipes[r.ID]
	if !ok || rb.Commit != r.Commit {
		return true, nil
	}
	for _, arch := range r.Arches {
		ab, ok := rb.Arches[arch]
		if !ok || len(ab.RPMs) == 0 {
			return true, nil
		}
		for _, rpm := range ab.RPMs {
			if !strings.HasPrefix(rpm.File, r.ID+"/") {
				return true, nil
			}
			ok, err := exists(ArchPrefix(version, arch) + rpm.File)
			if err != nil {
				return false, err
			}
			if !ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// PhoneReadableMetadata reports whether Sailfish libsolv can index this repomd.
// The phone build of libsolv rejects zstd primary metadata.
func PhoneReadableMetadata(repomd []byte) bool {
	return !bytes.Contains(repomd, []byte(".zst"))
}

func RepoReady(version string, arches []string, exists func(string) (bool, error)) (bool, error) {
	ok, err := exists(PublicKey)
	if err != nil || !ok {
		return false, err
	}
	for _, arch := range arches {
		for _, rel := range []string{"repodata/repomd.xml", "repodata/repomd.xml.asc", "repomd.xml.asc"} {
			ok, err := exists(ArchPrefix(version, arch) + rel)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	return true, nil
}

func Removed(recipes []recipe.Recipe, man Manifest) []string {
	have := map[string]bool{}
	for _, r := range recipes {
		have[r.ID] = true
	}
	var out []string
	for name := range man.Recipes {
		if !have[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func unionArches(recipes []recipe.Recipe) []string {
	var out []string
	seen := map[string]bool{}
	for _, arch := range recipe.AllArches {
		for _, r := range recipes {
			for _, got := range r.Arches {
				if got == arch && !seen[arch] {
					seen[arch] = true
					out = append(out, arch)
				}
			}
		}
	}
	return out
}

func manifestArches(man Manifest) []string {
	seen := map[string]bool{}
	var out []string
	for _, rb := range man.Recipes {
		for arch := range rb.Arches {
			if !seen[arch] {
				seen[arch] = true
				out = append(out, arch)
			}
		}
	}
	sort.Strings(out)
	return out
}

func ArchOf(file string) (string, error) {
	base := filepath.Base(file)
	if !strings.HasSuffix(base, ".rpm") {
		return "", fmt.Errorf("not an rpm: %s", file)
	}
	name := strings.TrimSuffix(base, ".rpm")
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return "", fmt.Errorf("rpm name has no arch: %s", file)
	}
	arch := name[i+1:]
	switch arch {
	case "aarch64", "armv7hl", "i486", "noarch":
		return arch, nil
	default:
		return "", fmt.Errorf("unknown arch %s in %s", arch, file)
	}
}

func toDeps(r RPMRec) deps.RPM {
	return deps.RPM{
		File:     r.File,
		Name:     r.Name,
		Version:  r.Version,
		Release:  r.Release,
		Summary:  r.Summary,
		Provides: r.Provides,
		Requires: r.Requires,
	}
}

func fromDeps(r deps.RPM) RPMRec {
	return RPMRec{
		File:     r.File,
		Name:     r.Name,
		Version:  r.Version,
		Release:  r.Release,
		Summary:  r.Summary,
		Provides: append([]string{}, r.Provides...),
		Requires: append([]string{}, r.Requires...),
	}
}

func ourNames(man Manifest, built []deps.RPM) map[string]struct{} {
	names := map[string]struct{}{}
	add := func(name string, provides []string) {
		if name != "" {
			names[name] = struct{}{}
		}
		for _, p := range provides {
			if p != "" {
				names[p] = struct{}{}
			}
		}
	}
	for _, rb := range man.Recipes {
		for _, ab := range rb.Arches {
			for _, rpm := range ab.RPMs {
				add(rpm.Name, rpm.Provides)
			}
		}
	}
	for _, rpm := range built {
		add(rpm.Name, rpm.Provides)
	}
	return names
}

func publishedExcept(man Manifest, arch, skip string) []deps.RPM {
	var out []deps.RPM
	for name, rb := range man.Recipes {
		if name == skip {
			continue
		}
		for _, rpm := range rb.Arches[arch].RPMs {
			out = append(out, toDeps(rpm))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}
