// Package ftuna reports whether an RPM object was built by this project.
// Names in the recipe overlay's .sfosbuild/image mean the image was
// prepared with extra hooks, so the build was not fully offline.
// Packages that were not built here abstain.
package ftuna

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
)

// Check inspects the object key. Packages outside repo/ftuna-oss/ and scratch/
// abstain. Detail on ftuna.built is clean, hooks, or unknown.
// ftuna.image lists .sfosbuild/image entry names, one per line.
// A recipe with no image hooks is green. Hooks are red. A recipe that
// cannot be read is yellow.
func Check(key, recipesRoot string) []check.Issue {
	id, built := recipeID(key)
	if !built {
		return nil
	}
	builtIssue := check.Issue{Module: "ftuna", Path: key, Code: "ftuna.built", Detail: "unknown", Color: check.ColorYellow}
	if id == "" || recipesRoot == "" || !safeID(id) {
		return []check.Issue{builtIssue}
	}
	builtIssue.Path = id
	recipeDir := filepath.Join(recipesRoot, filepath.FromSlash(id))
	st, err := os.Stat(recipeDir)
	if err != nil || !st.IsDir() {
		return []check.Issue{builtIssue}
	}
	names, err := imageNames(filepath.Join(recipeDir, "overlay", ".sfosbuild", "image"))
	if err != nil {
		return []check.Issue{builtIssue}
	}
	if len(names) == 0 {
		builtIssue.Detail = "clean"
		builtIssue.Color = check.ColorGreen
		return []check.Issue{builtIssue}
	}
	builtIssue.Detail = "hooks"
	builtIssue.Color = check.ColorRed
	return []check.Issue{builtIssue, {
		Module: "ftuna",
		Path:   id,
		Code:   "ftuna.image",
		Detail: strings.Join(names, "\n"),
		Color:  check.ColorRed,
	}}
}

var (
	recipesOnce sync.Once
	recipesDir  string
)

// RecipesDir is the directory named by FTUNA_RECIPES.
// An empty string means image hooks cannot be listed.
func RecipesDir() string {
	recipesOnce.Do(func() {
		dir := strings.TrimSpace(os.Getenv("FTUNA_RECIPES"))
		if dir == "" {
			log.Printf("FTUNA_RECIPES is unset; FTuna image hooks will not be listed")
			return
		}
		st, err := os.Stat(dir)
		if err != nil {
			log.Printf("FTUNA_RECIPES %s: %v; FTuna image hooks will not be listed", dir, err)
			return
		}
		if !st.IsDir() {
			log.Printf("FTUNA_RECIPES %s is not a directory; FTuna image hooks will not be listed", dir)
			return
		}
		recipesDir = dir
	})
	return recipesDir
}

func recipeID(key string) (id string, built bool) {
	key = strings.Trim(key, "/")
	var rest string
	switch {
	case strings.HasPrefix(key, "repo/ftuna-oss/"):
		rest = strings.TrimPrefix(key, "repo/ftuna-oss/")
		built = true
	case strings.HasPrefix(key, "scratch/"):
		rest = strings.TrimPrefix(key, "scratch/")
		built = true
	default:
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || !strings.HasSuffix(parts[len(parts)-1], ".rpm") {
		return "", true
	}
	return strings.Join(parts[2:len(parts)-1], "/"), true
}

func safeID(id string) bool {
	if id == "" || filepath.IsAbs(id) {
		return false
	}
	for _, part := range strings.Split(id, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func imageNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || name == ".gitkeep" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
