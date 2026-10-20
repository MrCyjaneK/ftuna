// Package recipe reads the build recipe tree.
package recipe

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mrcyjanek/ftuna/internal/build/cdn"
	"gopkg.in/yaml.v3"
)

// AllArches is what sfosbuild builds for "all".
var AllArches = []string{"aarch64", "armv7hl", "i486"}

type Recipe struct {
	ID      string
	Name    string
	Dir     string
	Commit  string
	Arches  []string
	URL     string
	Patches string
	Overlay string
	// Path is the directory inside the clone that contains rpm/.
	// "." is the repository root. apps/sfos is that subdirectory.
	Path        string
	Icon        string
	Screenshots []string
	Install     []string
	// Description is the package text, written as markdown.
	Description string
	// AI is the Chum AIRating: H, A, B, C, V, or X.
	AI string
	// File is the recipe yaml. build.yaml keeps the directory as its ID.
	// Another file in that directory, such as libfoo.yaml, is a second
	// package from the same clone and its ID ends with /libfoo.
	File string
	// Repos are extra rpm repositories installed into the SDK image.
	// A URL may be followed by ; and an https signing key.
	// The key is written as gpgkey= so zypper imports it on refresh.
	Repos []string
}

type recipeFile struct {
	Name        string   `yaml:"name"`
	Commit      string   `yaml:"commit"`
	Arches      string   `yaml:"arches"`
	Icon        string   `yaml:"icon"`
	Screenshots []string `yaml:"screenshots"`
	Install     []string `yaml:"install"`
	Description string   `yaml:"description"`
	AI          string   `yaml:"ai"`
	Path        string   `yaml:"path"`
	Repos       []string `yaml:"repos"`
	Overlay     string   `yaml:"overlay"`
	Patches     string   `yaml:"patches"`
}

// Root is the recipes checkout named by FTUNA_RECIPES.
func Root() (string, error) {
	dir := strings.TrimSpace(os.Getenv("FTUNA_RECIPES"))
	if dir == "" {
		return "", fmt.Errorf("FTUNA_RECIPES is unset")
	}
	st, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("FTUNA_RECIPES %s: %w", dir, err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("FTUNA_RECIPES %s is not a directory", dir)
	}
	return dir, nil
}

func Discover(root string) ([]Recipe, error) {
	var out []Recipe
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "overlay", "patches", ".git", ".github":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		r, err := load(root, path)
		if err != nil {
			return err
		}
		out = append(out, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	seen := map[string]string{}
	for _, r := range out {
		if prev, ok := seen[r.ID]; ok {
			return nil, fmt.Errorf("duplicate recipe %s (%s and %s)", r.ID, prev, r.File)
		}
		seen[r.ID] = r.File
	}
	return out, nil
}

// recipeStem is the filename of a second package in a repository directory.
var recipeStem = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func recipeID(dir, base string) (string, error) {
	if base == "build.yaml" {
		return dir, nil
	}
	stem := strings.TrimSuffix(base, ".yaml")
	if !recipeStem.MatchString(stem) {
		return "", fmt.Errorf("recipe file %s: use build.yaml, or a name of letters, digits, '.', '_' and '-'", base)
	}
	return dir + "/" + stem, nil
}

func load(root, yamlPath string) (Recipe, error) {
	dir := filepath.Dir(yamlPath)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return Recipe{}, err
	}
	source := filepath.ToSlash(rel)
	if source == "" || source == "." || strings.HasPrefix(source, "../") || strings.Contains(source, "/../") {
		return Recipe{}, fmt.Errorf("%s: recipe path %q is not under %s", yamlPath, source, root)
	}
	id, err := recipeID(source, filepath.Base(yamlPath))
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", source, err)
	}
	body, err := os.ReadFile(yamlPath)
	if err != nil {
		return Recipe{}, err
	}
	var file recipeFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	displayName, err := parseName(file.Name)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	if !fullHash(file.Commit) {
		return Recipe{}, fmt.Errorf("%s: commit must be a 40-character hex hash", id)
	}
	arches, err := parseArches(file.Arches)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	ai, err := parseAI(file.AI)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	description, err := parseDescription(file.Description)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	icon := strings.TrimSpace(file.Icon)
	if icon != "" {
		if _, err := cdn.ParseAsset(icon); err != nil {
			return Recipe{}, fmt.Errorf("%s: icon: %w", id, err)
		}
	}
	shots, err := cleanURLs(id, "screenshots", file.Screenshots)
	if err != nil {
		return Recipe{}, err
	}
	install, err := cleanNames(id, file.Install)
	if err != nil {
		return Recipe{}, err
	}
	buildPath, err := cleanPath(file.Path)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	repos, err := cleanRepos(id, file.Repos)
	if err != nil {
		return Recipe{}, err
	}
	overlayRel, err := cleanRelDir("overlay", file.Overlay, "overlay")
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	patchesRel, err := cleanRelDir("patches", file.Patches, "patches")
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", id, err)
	}
	return Recipe{
		ID:          id,
		Name:        displayName,
		Dir:         dir,
		Commit:      file.Commit,
		Arches:      arches,
		URL:         "https://" + source,
		Patches:     filepath.Join(dir, filepath.FromSlash(patchesRel)),
		Overlay:     filepath.Join(dir, filepath.FromSlash(overlayRel)),
		Path:        buildPath,
		Icon:        icon,
		Screenshots: shots,
		Install:     install,
		Description: description,
		AI:          ai,
		File:        yamlPath,
		Repos:       repos,
	}, nil
}

// cleanPath is the directory inside the clone that contains rpm/.
// "." is the repository root. ./apps/sfos and apps/sfos both name that subdirectory.
func cleanPath(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(s) {
		return "", fmt.Errorf("path %q must be a directory inside the repository", s)
	}
	clean := filepath.ToSlash(filepath.Clean(s))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path %q must be a directory inside the repository", s)
	}
	if strings.HasSuffix(clean, ".yaml") || strings.HasSuffix(clean, ".spec") {
		return "", fmt.Errorf("path %q must be the directory that contains rpm/", s)
	}
	return clean, nil
}

// cleanRelDir is a directory next to the recipe yaml, such as overlay or
// go1_18/overlay. An empty value uses fallback.
func cleanRelDir(field, s, fallback string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	if filepath.IsAbs(s) {
		return "", fmt.Errorf("%s %q must be a directory inside the recipe", field, s)
	}
	clean := filepath.ToSlash(filepath.Clean(s))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%s %q must be a directory inside the recipe", field, s)
	}
	return clean, nil
}

func parseName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("name is required")
	}
	return s, nil
}

func parseDescription(s string) (string, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return "", fmt.Errorf("description is required")
	}
	return s, nil
}

func parseAI(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case "H", "A", "B", "C", "V", "X":
		return strings.TrimSpace(s), nil
	case "":
		return "", fmt.Errorf("ai is required (H, A, B, C, V, or X)")
	default:
		return "", fmt.Errorf("ai must be H, A, B, C, V, or X")
	}
}

func cleanURLs(recipeName, field string, urls []string) ([]string, error) {
	if urls == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("%s: %s contains an empty URL", recipeName, field)
		}
		if _, err := cdn.ParseAsset(raw); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", recipeName, field, err)
		}
		if seen[raw] {
			return nil, fmt.Errorf("%s: duplicate %s URL %s", recipeName, field, raw)
		}
		seen[raw] = true
		out = append(out, raw)
	}
	return out, nil
}

func cleanNames(recipeName string, names []string) ([]string, error) {
	if names == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " /\\") {
			return nil, fmt.Errorf("%s: invalid install package %q", recipeName, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("%s: duplicate install package %s", recipeName, name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// Updated is the committer date of the latest commit that touched the recipe yaml.
// A file that has never been committed uses its modification time.
func Updated(repoRoot, yamlPath string) (time.Time, error) {
	rel, err := filepath.Rel(repoRoot, yamlPath)
	if err != nil {
		return time.Time{}, err
	}
	cmd := exec.Command("git", "-C", repoRoot, "log", "-1", "--format=%cI", "--", rel)
	out, err := cmd.Output()
	if err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return time.Time{}, fmt.Errorf("%s: %w", rel, err)
			}
			return t, nil
		}
	}
	st, err := os.Stat(yamlPath)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

func parseArches(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "all" {
		return append([]string{}, AllArches...), nil
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		arch := strings.TrimSpace(part)
		if !knownArch(arch) {
			return nil, fmt.Errorf("unknown arch %q", arch)
		}
		if seen[arch] {
			return nil, fmt.Errorf("duplicate arch %s", arch)
		}
		seen[arch] = true
		out = append(out, arch)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no architectures")
	}
	return out, nil
}

func knownArch(arch string) bool {
	for _, a := range AllArches {
		if a == arch {
			return true
		}
	}
	return false
}

func fullHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func ValidVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func ArchArg(arches []string) string {
	if len(arches) == len(AllArches) {
		same := true
		for i, a := range AllArches {
			if arches[i] != a {
				same = false
				break
			}
		}
		if same {
			return "all"
		}
	}
	return strings.Join(arches, ",")
}
