// Package catalog builds the info.json document served with each repository.
package catalog

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrcyjanek/ftuna/internal/aibadge"
	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
	"github.com/mrcyjanek/ftuna/internal/xzjson"
)

type Doc struct {
	Version      int      `json:"version"`
	Updated      string   `json:"updated"`
	Categories   []string `json:"categories"`
	Repositories []Repo   `json:"repositories"`
	Packages     []Pkg    `json:"packages"`
}

type Repo struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type Pkg struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"`
	Updated     string   `json:"updated"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Screenshots []string `json:"screenshots"`
	Repo        string   `json:"repo"`
	Status      Status   `json:"status"`
	RPMs        []RPM    `json:"rpms"`
}

type Status struct {
	Sailjail    string   `json:"sailjail"`
	Setuid      []Setuid `json:"setuid"`
	Permissions []string `json:"permissions"`
	RedFlags    []Flag   `json:"red_flags"`
}

type Setuid struct {
	Path string `json:"path"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
}

type Flag struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
}

type RPM struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Release  string   `json:"release"`
	Arch     string   `json:"arch"`
	Install  bool     `json:"install"`
	Requires []string `json:"requires"`
	SHA256   string   `json:"sha256,omitempty"`
	// Href is the RPM's path inside the repository.
	Href string `json:"href,omitempty"`
}

// Built is one architecture of a recipe, already placed in a native rpm repository.
type Built struct {
	Arch    string
	RepoID  string
	RepoURL string
	RPMs    []deps.RPM
}

// Item is one recipe plus the CDN URLs for its images.
type Item struct {
	Recipe      recipe.Recipe
	Updated     time.Time
	Icon        string
	Screenshots []string
	Built       []Built
}

func Assemble(items []Item) Doc {
	doc := Doc{
		Version:      1,
		Categories:   []string{"application", "library"},
		Repositories: []Repo{},
		Packages:     []Pkg{},
	}
	var newest time.Time
	repos := map[string]Repo{}
	for _, item := range items {
		when := item.Updated.UTC()
		for _, built := range item.Built {
			if len(built.RPMs) == 0 {
				continue
			}
			if when.After(newest) {
				newest = when
			}
			repos[built.RepoID] = Repo{ID: built.RepoID, URL: built.RepoURL}
			doc.Packages = append(doc.Packages, packageOf(item, built, when))
		}
	}
	if !newest.IsZero() {
		doc.Updated = newest.Format(time.RFC3339)
	}
	for _, repo := range repos {
		doc.Repositories = append(doc.Repositories, repo)
	}
	sort.Slice(doc.Repositories, func(i, j int) bool { return doc.Repositories[i].ID < doc.Repositories[j].ID })
	sort.Slice(doc.Packages, func(i, j int) bool {
		if doc.Packages[i].ID != doc.Packages[j].ID {
			return doc.Packages[i].ID < doc.Packages[j].ID
		}
		return doc.Packages[i].Repo < doc.Packages[j].Repo
	})
	return doc
}

func packageOf(item Item, built Built, updated time.Time) Pkg {
	listed := item.Recipe.Install != nil
	rows := append([]deps.RPM{}, built.RPMs...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	summary := strings.TrimSpace(item.Recipe.Name)
	rpms := make([]RPM, 0, len(rows))
	for _, row := range rows {
		arch := built.Arch
		if a, err := archOf(row.File); err == nil {
			arch = a
		}
		rpms = append(rpms, RPM{
			Name:     row.Name,
			Version:  row.Version,
			Release:  row.Release,
			Arch:     arch,
			Install:  checked(row.Name, item.Recipe.Install, listed),
			Requires: cleanRequires(row.Requires),
			SHA256:   row.SHA256,
			Href:     row.File,
		})
	}
	category := "library"
	if item.Icon != "" {
		category = "application"
	}
	shots := item.Screenshots
	if shots == nil {
		shots = []string{}
	}
	return Pkg{
		ID:          item.Recipe.ID,
		Category:    category,
		Updated:     updated.Format(time.RFC3339),
		Summary:     summary,
		Description: aibadge.Append(item.Recipe.Description, item.Recipe.AI),
		Icon:        item.Icon,
		Screenshots: shots,
		Repo:        built.RepoID,
		Status: Status{
			Sailjail:    "unknown",
			Setuid:      []Setuid{},
			Permissions: []string{},
			RedFlags:    []Flag{},
		},
		RPMs: rpms,
	}
}

func checked(name string, install []string, listed bool) bool {
	if listed {
		for _, want := range install {
			if want == name {
				return true
			}
		}
		return false
	}
	return !strings.HasSuffix(name, "-debuginfo") && !strings.HasSuffix(name, "-debugsource") && !strings.HasSuffix(name, "-doc")
}

func cleanRequires(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, spec := range in {
		name := deps.Capability(spec)
		if name == "" || strings.HasPrefix(name, "rpmlib(") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func archOf(file string) (string, error) {
	base := file
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if !strings.HasSuffix(base, ".rpm") {
		return "", fmt.Errorf("not an rpm: %s", file)
	}
	name := strings.TrimSuffix(base, ".rpm")
	i := strings.LastIndex(name, ".")
	if i <= 0 {
		return "", fmt.Errorf("rpm name has no arch: %s", file)
	}
	return name[i+1:], nil
}

func Encode(doc Doc) ([]byte, error) {
	return xzjson.Encode(doc)
}
