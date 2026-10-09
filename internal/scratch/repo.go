package scratch

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/build/rpmmd"
)

var scratchArches = []string{"aarch64", "armv7hl", "i486"}

type rpmFile struct {
	abs string
	rel string
}

// stageRepo returns the directory to upload. A tree that already has
// repodata/repomd.xml for every architecture that contains an RPM is
// returned unchanged. Otherwise a temporary repository is built: loose
// RPM files are placed under the architecture in their filename, and
// repodata is written for each architecture. noarch packages are copied
// into every architecture in the repository. When every package is
// noarch, that is aarch64, armv7hl, and i486.
func stageRepo(src string) (string, func(), error) {
	rpms, info, err := listRPMs(src)
	if err != nil {
		return "", nil, err
	}
	if len(rpms) == 0 {
		return "", nil, fmt.Errorf("scratch: no rpms in %s", src)
	}
	archLayout, err := oneLayout(rpms)
	if err != nil {
		return "", nil, err
	}
	if archLayout && repoReady(src, rpms) {
		return src, func() {}, nil
	}
	dst, err := os.MkdirTemp("", "ftuna-scratch-repo-")
	if err != nil {
		return "", nil, err
	}
	if err := fillRepo(dst, rpms, info, archLayout); err != nil {
		os.RemoveAll(dst)
		return "", nil, err
	}
	return dst, func() { os.RemoveAll(dst) }, nil
}

func listRPMs(src string) ([]rpmFile, string, error) {
	var rpms []rpmFile
	info := ""
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("scratch: symlink %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(rel, "/repodata/") {
			return nil
		}
		if rel == "info.json.xz" {
			info = path
			return nil
		}
		if strings.HasSuffix(rel, ".rpm") {
			rpms = append(rpms, rpmFile{abs: path, rel: rel})
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return rpms, info, nil
}

func oneLayout(rpms []rpmFile) (bool, error) {
	archN, looseN := 0, 0
	for _, rpm := range rpms {
		if _, ok := archOfPath(rpm.rel); ok {
			archN++
			continue
		}
		looseN++
	}
	if archN > 0 && looseN > 0 {
		return false, fmt.Errorf("scratch: put every rpm under aarch64, armv7hl, or i486, or keep them all together")
	}
	return archN > 0, nil
}

func archOfPath(rel string) (string, bool) {
	arch, rest, ok := strings.Cut(rel, "/")
	if !ok || rest == "" || strings.HasPrefix(rest, "repodata/") {
		return "", false
	}
	switch arch {
	case "aarch64", "armv7hl", "i486":
		return arch, true
	default:
		return "", false
	}
}

func repoReady(src string, rpms []rpmFile) bool {
	seen := map[string]bool{}
	for _, rpm := range rpms {
		arch, ok := archOfPath(rpm.rel)
		if !ok {
			return false
		}
		seen[arch] = true
	}
	for arch := range seen {
		st, err := os.Stat(filepath.Join(src, arch, "repodata", "repomd.xml"))
		if err != nil || st.IsDir() {
			return false
		}
	}
	return true
}

func fillRepo(dst string, rpms []rpmFile, info string, archLayout bool) error {
	seen := map[string]bool{}
	place := func(src, rel string) error {
		if seen[rel] {
			return fmt.Errorf("scratch: duplicate %s", rel)
		}
		seen[rel] = true
		return linkOrCopy(src, filepath.Join(dst, filepath.FromSlash(rel)))
	}
	if info != "" {
		if err := place(info, "info.json.xz"); err != nil {
			return err
		}
	}
	if archLayout {
		arches := map[string]bool{}
		for _, rpm := range rpms {
			arch, _ := archOfPath(rpm.rel)
			arches[arch] = true
			if err := place(rpm.abs, rpm.rel); err != nil {
				return err
			}
		}
		for arch := range arches {
			if err := rpmmd.Write(filepath.Join(dst, arch)); err != nil {
				return err
			}
		}
		return nil
	}
	targets := map[string]bool{}
	var noarch []rpmFile
	for _, rpm := range rpms {
		arch, err := archOfName(rpm.rel)
		if err != nil {
			return fmt.Errorf("scratch: %w", err)
		}
		if arch == "noarch" {
			noarch = append(noarch, rpm)
			continue
		}
		targets[arch] = true
		if err := place(rpm.abs, arch+"/"+filepath.Base(rpm.rel)); err != nil {
			return err
		}
	}
	if len(targets) == 0 {
		for _, arch := range scratchArches {
			targets[arch] = true
		}
	}
	for _, rpm := range noarch {
		base := filepath.Base(rpm.rel)
		for arch := range targets {
			if err := place(rpm.abs, arch+"/"+base); err != nil {
				return err
			}
		}
	}
	for arch := range targets {
		if err := rpmmd.Write(filepath.Join(dst, arch)); err != nil {
			return err
		}
	}
	return nil
}

func archOfName(rel string) (string, error) {
	base := filepath.Base(rel)
	if !strings.HasSuffix(base, ".rpm") {
		return "", fmt.Errorf("not an rpm: %s", rel)
	}
	name := strings.TrimSuffix(base, ".rpm")
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return "", fmt.Errorf("rpm name has no arch: %s", rel)
	}
	arch := name[i+1:]
	switch arch {
	case "aarch64", "armv7hl", "i486", "noarch":
		return arch, nil
	default:
		return "", fmt.Errorf("unknown arch %s in %s", arch, rel)
	}
}

func linkOrCopy(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
