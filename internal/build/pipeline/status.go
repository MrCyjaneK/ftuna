package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const statusVersion = 1

const (
	stateBuilt    = "built"
	stateCurrent  = "current"
	stateFailed   = "failed"
	stateSkipped  = "skipped"
	statePending  = "pending"
	stateBuilding = "building"
)

func StatusKey(version string) string {
	return PublicRoot + version + "/status.json"
}

// Status is the document served at repo/ftuna-oss/<version>/status.json.
type Status struct {
	Version int            `json:"version"`
	SFOS    string         `json:"sfos"`
	Updated string         `json:"updated"`
	Summary StatusSummary  `json:"summary"`
	Recipes []StatusRecipe `json:"recipes"`
}

type StatusSummary struct {
	Recipes  int `json:"recipes"`
	Built    int `json:"built"`
	Current  int `json:"current"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
	Pending  int `json:"pending"`
	Building int `json:"building"`
}

type StatusRecipe struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Path        string   `json:"path"`
	Commit      string   `json:"commit"`
	Arches      []string `json:"arches"`
	Repos       []string `json:"repos,omitempty"`
	Description string   `json:"description"`
	Icon        string   `json:"icon,omitempty"`
	Screenshots []string `json:"screenshots,omitempty"`
	AI          string   `json:"ai"`
	Fingerprint string   `json:"fingerprint"`
	State       string   `json:"state"`
	// Error, Log, and FailedAt are kept for failed and skipped recipes.
	// Skipped means the fingerprint still matches a failure from an earlier run.
	Error           string              `json:"error,omitempty"`
	FailedAt        string              `json:"failed_at,omitempty"`
	Log             string              `json:"log,omitempty"`
	RPMs            map[string][]RPMRec `json:"rpms,omitempty"`
	PublishedCommit string              `json:"published_commit,omitempty"`
}

// Fingerprint is the sha256 of the inputs that decide whether a recipe
// is built again. Sections are length-prefixed: the recipe yaml, patch files,
// overlay files, the sfosbuild path, the arch argument, and the SFOS version.
// A present patches or overlay directory is part of the hash, because checkout
// treats an empty overlay differently from a missing one.
func Fingerprint(r recipe.Recipe, version string) (string, error) {
	body, err := os.ReadFile(r.File)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if err := writePart(h, "recipe", body); err != nil {
		return "", err
	}
	if err := writeFiles(h, "patches", r.Patches, true); err != nil {
		return "", err
	}
	if err := writeFiles(h, "overlay", r.Overlay, false); err != nil {
		return "", err
	}
	if err := writePart(h, "path", []byte(r.Path)); err != nil {
		return "", err
	}
	if err := writePart(h, "arch", []byte(recipe.ArchArg(r.Arches))); err != nil {
		return "", err
	}
	if err := writePart(h, "version", []byte(version)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writePart(w io.Writer, name string, body []byte) error {
	if _, err := fmt.Fprintf(w, "%s\n%d\n", name, len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

type hashedFile struct {
	abs string
	rel string
}

func writeFiles(w io.Writer, name, dir string, patchOnly bool) error {
	present, files, err := inputFiles(dir, patchOnly)
	if err != nil {
		return err
	}
	flag := "0"
	if present {
		flag = "1"
	}
	if err := writePart(w, name+"-present", []byte(flag)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\n%d\n", name, len(files)); err != nil {
		return err
	}
	for _, f := range files {
		body, err := os.ReadFile(f.abs)
		if err != nil {
			return err
		}
		if err := writePart(w, f.rel, body); err != nil {
			return err
		}
	}
	return nil
}

func inputFiles(dir string, patchOnly bool) (bool, []hashedFile, error) {
	if dir == "" {
		return false, nil, nil
	}
	st, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if !st.IsDir() {
		return false, nil, fmt.Errorf("%s is not a directory", dir)
	}
	var out []hashedFile
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if patchOnly && !strings.HasSuffix(d.Name(), ".patch") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, hashedFile{abs: path, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return false, nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return true, out, nil
}

// failureUnchanged reports whether prev is a recorded failure of fingerprint.
func failureUnchanged(prev StatusRecipe, fingerprint string) bool {
	if prev.Fingerprint != fingerprint {
		return false
	}
	return prev.State == stateFailed || prev.State == stateSkipped
}

// prepareStatus decides each recipe's state from the previous status file and
// the manifest. Recipes in the returned slice are the ones All should build.
// An unchanged failure is skipped and keeps its error, log, and failed_at.
func prepareStatus(recipes []recipe.Recipe, man Manifest, prev Status, version string, exists func(string) (bool, error)) (Status, []recipe.Recipe, error) {
	prevByID := map[string]StatusRecipe{}
	for _, entry := range prev.Recipes {
		prevByID[entry.ID] = entry
	}
	doc := Status{
		Version: statusVersion,
		SFOS:    version,
		Recipes: make([]StatusRecipe, 0, len(recipes)),
	}
	var build []recipe.Recipe
	for _, r := range recipes {
		fp, err := Fingerprint(r, version)
		if err != nil {
			return Status{}, nil, fmt.Errorf("%s: %w", r.ID, err)
		}
		entry := statusEntry(r, fp)
		if old, ok := prevByID[r.ID]; ok && failureUnchanged(old, fp) {
			entry.State = stateSkipped
			entry.Error = old.Error
			entry.Log = old.Log
			entry.FailedAt = old.FailedAt
			applyPublished(&entry, man)
			doc.Recipes = append(doc.Recipes, entry)
			continue
		}
		need, err := NeedsBuild(r, man, version, false, exists)
		if err != nil {
			return Status{}, nil, err
		}
		// The manifest only records the commit and RPM names. A spec or overlay
		// edit keeps those the same, so compare the fingerprint recorded when
		// this recipe was last considered.
		if !need {
			if old, ok := prevByID[r.ID]; ok && old.Fingerprint != "" && old.Fingerprint != fp {
				need = true
			}
		}
		applyPublished(&entry, man)
		if !need {
			entry.State = stateCurrent
			doc.Recipes = append(doc.Recipes, entry)
			continue
		}
		entry.State = statePending
		doc.Recipes = append(doc.Recipes, entry)
		build = append(build, r)
	}
	doc.Summary = summarize(doc.Recipes)
	return doc, build, nil
}

func statusEntry(r recipe.Recipe, fp string) StatusRecipe {
	return StatusRecipe{
		ID:          r.ID,
		Name:        r.Name,
		URL:         r.URL,
		Path:        r.Path,
		Commit:      r.Commit,
		Arches:      append([]string{}, r.Arches...),
		Repos:       append([]string{}, r.Repos...),
		Description: r.Description,
		Icon:        r.Icon,
		Screenshots: append([]string{}, r.Screenshots...),
		AI:          r.AI,
		Fingerprint: fp,
	}
}

func applyPublished(entry *StatusRecipe, man Manifest) {
	rb, ok := man.Recipes[entry.ID]
	if !ok {
		return
	}
	entry.PublishedCommit = rb.Commit
	if len(rb.Arches) == 0 {
		return
	}
	rpms := map[string][]RPMRec{}
	for arch, ab := range rb.Arches {
		if len(ab.RPMs) == 0 {
			continue
		}
		recs := append([]RPMRec{}, ab.RPMs...)
		sort.Slice(recs, func(i, j int) bool { return recs[i].File < recs[j].File })
		rpms[arch] = recs
	}
	if len(rpms) > 0 {
		entry.RPMs = rpms
	}
}

func rpmsFromPlaced(placed map[string][]deps.RPM) map[string][]RPMRec {
	if len(placed) == 0 {
		return nil
	}
	out := map[string][]RPMRec{}
	for arch, rpms := range placed {
		var recs []RPMRec
		for _, rpm := range rpms {
			recs = append(recs, fromDeps(rpm))
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].File < recs[j].File })
		out[arch] = recs
	}
	return out
}

func noteFailure(doc *Status, i int, err error, buildLog string) {
	log.Printf("fail %s: %v", doc.Recipes[i].ID, err)
	doc.Recipes[i].State = stateFailed
	doc.Recipes[i].Error = err.Error()
	doc.Recipes[i].Log = strings.ToValidUTF8(buildLog, "\uFFFD")
	doc.Recipes[i].FailedAt = time.Now().UTC().Format(time.RFC3339)
}

func noteBuilt(doc *Status, i int, r recipe.Recipe, placed map[string][]deps.RPM) {
	doc.Recipes[i].State = stateBuilt
	doc.Recipes[i].Error = ""
	doc.Recipes[i].Log = ""
	doc.Recipes[i].FailedAt = ""
	doc.Recipes[i].PublishedCommit = r.Commit
	doc.Recipes[i].RPMs = rpmsFromPlaced(placed)
}

func summarize(recipes []StatusRecipe) StatusSummary {
	var s StatusSummary
	s.Recipes = len(recipes)
	for _, r := range recipes {
		switch r.State {
		case stateBuilt:
			s.Built++
		case stateCurrent:
			s.Current++
		case stateFailed:
			s.Failed++
		case stateSkipped:
			s.Skipped++
		case statePending:
			s.Pending++
		case stateBuilding:
			s.Building++
		}
	}
	return s
}

func loadStatus(ctx context.Context, store *s3store.Store, version string) (Status, error) {
	body, ok, err := store.GetIfExists(ctx, StatusKey(version))
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{Recipes: []StatusRecipe{}}, nil
	}
	var doc Status
	if err := json.Unmarshal(body, &doc); err != nil {
		return Status{}, fmt.Errorf("status %s: %w", version, err)
	}
	if doc.Recipes == nil {
		doc.Recipes = []StatusRecipe{}
	}
	return doc, nil
}

func putStatus(ctx context.Context, store *s3store.Store, version string, doc *Status) error {
	doc.Version = statusVersion
	doc.SFOS = version
	doc.Updated = time.Now().UTC().Format(time.RFC3339)
	doc.Summary = summarize(doc.Recipes)
	if doc.Recipes == nil {
		doc.Recipes = []StatusRecipe{}
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = store.PutBytes(ctx, StatusKey(version), "application/json", raw, nil)
	return err
}
