package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrcyjanek/ftuna/internal/build/recipe"
)

func testRecipe(t *testing.T) recipe.Recipe {
	t.Helper()
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "build.yaml")
	if err := os.WriteFile(yamlPath, []byte("name: App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "patches"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "patches", "a.patch"), []byte("diff a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "patches", "notes.txt"), []byte("ignore me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "overlay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "overlay", "b.txt"), []byte("overlay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return recipe.Recipe{
		ID:      "github.com/example/app",
		Name:    "App",
		File:    yamlPath,
		Patches: filepath.Join(dir, "patches"),
		Overlay: filepath.Join(dir, "overlay"),
		Path:    ".",
		Arches:  append([]string{}, recipe.AllArches...),
		Commit:  "0123456789abcdef0123456789abcdef01234567",
	}
}

func TestFingerprintStableAndSensitive(t *testing.T) {
	r := testRecipe(t)
	a, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("fingerprint changed without an edit: %s %s", a, b)
	}
	if err := os.WriteFile(filepath.Join(r.Patches, "notes.txt"), []byte("still ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if notes != a {
		t.Fatal("files in patches/ that are not patches must not change the fingerprint")
	}

	r.Path = "apps/sfos"
	c, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatal("path change must change the fingerprint")
	}

	r.Path = "."
	r.Arches = []string{"aarch64"}
	d, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if d == a {
		t.Fatal("arch change must change the fingerprint")
	}

	r.Arches = append([]string{}, recipe.AllArches...)
	e, err := Fingerprint(r, "5.1.0.12")
	if err != nil {
		t.Fatal(err)
	}
	if e == a {
		t.Fatal("version change must change the fingerprint")
	}

	if err := os.WriteFile(r.File, []byte("name: App2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if f == a {
		t.Fatal("recipe change must change the fingerprint")
	}
	if err := os.WriteFile(r.File, []byte("name: App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if restored != a {
		t.Fatal("restored recipe must match the original fingerprint")
	}

	if err := os.WriteFile(filepath.Join(r.Patches, "a.patch"), []byte("diff b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if g == a {
		t.Fatal("patch change must change the fingerprint")
	}
	if err := os.WriteFile(filepath.Join(r.Patches, "a.patch"), []byte("diff a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(r.Overlay, "b.txt"), []byte("overlay2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if h == a {
		t.Fatal("overlay change must change the fingerprint")
	}
}

func TestPrepareStatusSkipsUnchangedFailure(t *testing.T) {
	r := testRecipe(t)
	fp, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	prev := Status{Recipes: []StatusRecipe{{
		ID:          r.ID,
		State:       stateFailed,
		Fingerprint: fp,
		Error:       "sfosbuild 5.1.0.11 all .: exit status 1",
		FailedAt:    "2026-01-02T03:04:05Z",
		Log:         "gcc: error: boom",
	}}}
	exists := func(string) (bool, error) { return false, nil }
	doc, build, err := prepareStatus([]recipe.Recipe{r}, emptyManifest(), prev, "5.1.0.11", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 {
		t.Fatal("unchanged failure must not be built again")
	}
	if len(doc.Recipes) != 1 {
		t.Fatalf("recipes %d", len(doc.Recipes))
	}
	got := doc.Recipes[0]
	if got.State != stateSkipped {
		t.Fatalf("state %s", got.State)
	}
	if got.Error != prev.Recipes[0].Error || got.Log != prev.Recipes[0].Log || got.FailedAt != prev.Recipes[0].FailedAt {
		t.Fatalf("error was not kept: %+v", got)
	}
	if got.Fingerprint != fp {
		t.Fatalf("fingerprint %s", got.Fingerprint)
	}
	if doc.Summary.Skipped != 1 || doc.Summary.Recipes != 1 {
		t.Fatalf("summary %+v", doc.Summary)
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Status
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Recipes[0].Error != prev.Recipes[0].Error || decoded.Recipes[0].Log != prev.Recipes[0].Log {
		t.Fatalf("json dropped the failure: %+v", decoded.Recipes[0])
	}

	doc, build, err = prepareStatus([]recipe.Recipe{r}, emptyManifest(), prev, "5.1.0.12", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 1 || doc.Recipes[0].State != statePending {
		t.Fatalf("changed version should build, state %s build %d", doc.Recipes[0].State, len(build))
	}

	prev.Recipes[0].State = stateBuilt
	doc, build, err = prepareStatus([]recipe.Recipe{r}, emptyManifest(), prev, "5.1.0.11", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 1 || doc.Recipes[0].State != statePending {
		t.Fatal("a successful record must not suppress a build")
	}

	prev.Recipes[0].State = stateFailed
	prev.Recipes[0].Fingerprint = fp
	doc, build, err = prepareStatus([]recipe.Recipe{r}, emptyManifest(), prev, "5.1.0.11", exists, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 1 || doc.Recipes[0].State != statePending {
		t.Fatal("--rebuild-failed should retry a skipped failure")
	}
}

func TestPrepareStatusRebuildFailedKeepsCurrent(t *testing.T) {
	r := testRecipe(t)
	fp, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	man := Manifest{Recipes: map[string]RecipeBuild{
		r.ID: {
			Commit: r.Commit,
			Arches: map[string]ArchBuild{
				"aarch64": {RPMs: []RPMRec{{File: r.ID + "/app.aarch64.rpm", Name: "app"}}},
				"armv7hl": {RPMs: []RPMRec{{File: r.ID + "/app.armv7hl.rpm", Name: "app"}}},
				"i486":    {RPMs: []RPMRec{{File: r.ID + "/app.i486.rpm", Name: "app"}}},
				"src":     {RPMs: []RPMRec{{File: r.ID + "/app.src.rpm", Name: "app"}}},
			},
		},
	}}
	prev := Status{Recipes: []StatusRecipe{{
		ID:          r.ID,
		State:       stateFailed,
		Fingerprint: fp,
		Error:       "compile failed",
		FailedAt:    "2026-03-04T05:06:07Z",
		Log:         "error: still broken",
	}}}
	exists := func(string) (bool, error) { return true, nil }
	doc, build, err := prepareStatus([]recipe.Recipe{r}, man, prev, "5.1.0.11", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 || doc.Recipes[0].State != stateSkipped {
		t.Fatalf("recorded failure should stay skipped, state %s build %d", doc.Recipes[0].State, len(build))
	}
	if doc.Recipes[0].Error != "compile failed" || doc.Recipes[0].RPMs["aarch64"][0].Name != "app" {
		t.Fatalf("failure and published rpms should stay: %+v", doc.Recipes[0])
	}

	doc, build, err = prepareStatus([]recipe.Recipe{r}, man, prev, "5.1.0.11", exists, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 1 || doc.Recipes[0].State != statePending {
		t.Fatalf("--rebuild-failed should build a recorded failure, state %s build %d", doc.Recipes[0].State, len(build))
	}
	if doc.Recipes[0].Error != "" || doc.Recipes[0].Log != "" || doc.Recipes[0].FailedAt != "" {
		t.Fatalf("retry should drop the old error: %+v", doc.Recipes[0])
	}
	if doc.Recipes[0].PublishedCommit != r.Commit || doc.Recipes[0].RPMs["aarch64"][0].Name != "app" {
		t.Fatalf("published packages should stay visible while rebuilding: %+v", doc.Recipes[0])
	}

	prev.Recipes[0].State = stateCurrent
	doc, build, err = prepareStatus([]recipe.Recipe{r}, man, prev, "5.1.0.11", exists, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 || doc.Recipes[0].State != stateCurrent {
		t.Fatalf("--rebuild-failed must leave a current recipe alone, state %s build %d", doc.Recipes[0].State, len(build))
	}
}

func TestPrepareStatusCurrentSkipsBuild(t *testing.T) {
	r := testRecipe(t)
	man := Manifest{Recipes: map[string]RecipeBuild{
		r.ID: {
			Commit: r.Commit,
			Arches: map[string]ArchBuild{
				"aarch64": {RPMs: []RPMRec{{File: r.ID + "/app.aarch64.rpm", Name: "app"}}},
				"armv7hl": {RPMs: []RPMRec{{File: r.ID + "/app.armv7hl.rpm", Name: "app"}}},
				"i486":    {RPMs: []RPMRec{{File: r.ID + "/app.i486.rpm", Name: "app"}}},
				"src":     {RPMs: []RPMRec{{File: r.ID + "/app.src.rpm", Name: "app"}}},
			},
		},
	}}
	doc, build, err := prepareStatus([]recipe.Recipe{r}, man, Status{}, "5.1.0.11", func(string) (bool, error) {
		return true, nil
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 || doc.Recipes[0].State != stateCurrent {
		t.Fatalf("state %s build %d", doc.Recipes[0].State, len(build))
	}
	if doc.Recipes[0].Error != "" || doc.Recipes[0].PublishedCommit != r.Commit {
		t.Fatalf("current recipe %+v", doc.Recipes[0])
	}
}

func TestPrepareStatusRebuildsWhenFingerprintChanges(t *testing.T) {
	r := testRecipe(t)
	man := Manifest{Recipes: map[string]RecipeBuild{
		r.ID: {
			Commit: r.Commit,
			Arches: map[string]ArchBuild{
				"aarch64": {RPMs: []RPMRec{{File: r.ID + "/app.aarch64.rpm", Name: "app"}}},
				"armv7hl": {RPMs: []RPMRec{{File: r.ID + "/app.armv7hl.rpm", Name: "app"}}},
				"i486":    {RPMs: []RPMRec{{File: r.ID + "/app.i486.rpm", Name: "app"}}},
				"src":     {RPMs: []RPMRec{{File: r.ID + "/app.src.rpm", Name: "app"}}},
			},
		},
	}}
	prev := Status{Recipes: []StatusRecipe{{
		ID:          r.ID,
		State:       stateCurrent,
		Fingerprint: "old",
	}}}
	doc, build, err := prepareStatus([]recipe.Recipe{r}, man, prev, "5.1.0.11", func(string) (bool, error) {
		return true, nil
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 1 || doc.Recipes[0].State != statePending {
		t.Fatalf("state %s build %d", doc.Recipes[0].State, len(build))
	}
}

func TestPrepareStatusKeepsPublishedRPMs(t *testing.T) {
	r := testRecipe(t)
	fp, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	man := Manifest{Recipes: map[string]RecipeBuild{
		r.ID: {
			Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Arches: map[string]ArchBuild{
				"aarch64": {RPMs: []RPMRec{{File: r.ID + "/app-1.aarch64.rpm", Name: "app", Version: "1"}}},
			},
		},
	}}
	prev := Status{Recipes: []StatusRecipe{{
		ID:          r.ID,
		State:       stateFailed,
		Fingerprint: fp,
		Error:       "compile failed",
		FailedAt:    "2026-03-04T05:06:07Z",
		Log:         "error: still broken",
	}}}
	doc, build, err := prepareStatus([]recipe.Recipe{r}, man, prev, "5.1.0.11", func(string) (bool, error) {
		return true, nil
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 {
		t.Fatal("unchanged failure must not replace the published build")
	}
	got := doc.Recipes[0]
	if got.PublishedCommit != man.Recipes[r.ID].Commit {
		t.Fatalf("published commit %s", got.PublishedCommit)
	}
	if got.RPMs["aarch64"][0].Name != "app" || got.Error != "compile failed" {
		t.Fatalf("status %+v", got)
	}
}

func TestGoBootstrapRebuildsDependents(t *testing.T) {
	root := t.TempDir()
	write := func(id, dep string) recipe.Recipe {
		t.Helper()
		dir := filepath.Join(root, filepath.FromSlash(id))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "name: " + id + "\ncommit: 0123456789abcdef0123456789abcdef01234567\n"
		yamlPath := filepath.Join(dir, "build.yaml")
		if err := os.WriteFile(yamlPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		r := recipe.Recipe{
			ID:      id,
			Name:    id,
			File:    yamlPath,
			Patches: filepath.Join(dir, "patches"),
			Overlay: filepath.Join(dir, "overlay"),
			Path:    ".",
			Arches:  append([]string{}, recipe.AllArches...),
			Commit:  "0123456789abcdef0123456789abcdef01234567",
		}
		if dep != "" {
			r.Depends = []string{dep}
		}
		return r
	}
	go18 := write("github.com/golang/go/go1_18", "")
	go19 := write("github.com/golang/go/go1_19", go18.ID)
	go21 := write("github.com/golang/go/go1_21", go19.ID)
	other := write("github.com/example/app", "")
	recipes := []recipe.Recipe{other, go18, go19, go21}

	fps, err := Fingerprints(recipes, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	own, err := Fingerprint(go18, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if fps[go18.ID] != own || fps[other.ID] == "" {
		t.Fatal("a recipe with no dependencies must keep its own fingerprint")
	}
	if fps[go19.ID] == fps[go18.ID] || fps[go21.ID] == fps[go19.ID] {
		t.Fatal("each Go in the bootstrap chain needs its own fingerprint")
	}
	if dep := unmetDependency(go19, map[string]bool{}); dep != go18.ID {
		t.Fatalf("unmet %s", dep)
	}
	if dep := unmetDependency(go21, map[string]bool{go19.ID: true}); dep != "" {
		t.Fatalf("go 1.21 should build once go 1.19 is published, got %s", dep)
	}

	man := publishedManifest(recipes)
	prev := Status{Recipes: []StatusRecipe{
		{ID: other.ID, State: stateCurrent, Fingerprint: fps[other.ID]},
		{ID: go18.ID, State: stateCurrent, Fingerprint: fps[go18.ID]},
		{ID: go19.ID, State: stateCurrent, Fingerprint: fps[go19.ID]},
		{ID: go21.ID, State: stateSkipped, Fingerprint: fps[go21.ID], Error: "dependency was not built", FailedAt: "2026-10-22T14:36:08Z"},
	}}
	exists := func(string) (bool, error) { return true, nil }
	doc, build, err := prepareStatus(recipes, man, prev, "5.1.0.11", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 || doc.Recipes[3].State != stateSkipped {
		t.Fatalf("unchanged chain should stay put, build %d state %s", len(build), doc.Recipes[3].State)
	}

	if err := os.WriteFile(go18.File, []byte("name: "+go18.ID+"\ncommit: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, build, err = prepareStatus(recipes, man, prev, "5.1.0.11", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range doc.Recipes {
		got[entry.ID] = entry.State
	}
	if got[go18.ID] != statePending || got[go19.ID] != statePending || got[go21.ID] != statePending {
		t.Fatalf("go chain should rebuild after go 1.18 changes: %+v", got)
	}
	if got[other.ID] != stateCurrent {
		t.Fatalf("unrelated recipe state %s", got[other.ID])
	}
	if len(build) != 3 {
		t.Fatalf("build %d", len(build))
	}
	ordered, err := recipe.Order(recipes)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	pending := map[string]bool{}
	for _, r := range build {
		pending[r.ID] = true
	}
	for _, r := range ordered {
		if pending[r.ID] {
			ids = append(ids, r.ID)
		}
	}
	want := []string{go18.ID, go19.ID, go21.ID}
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Fatalf("build order %v", ids)
	}

	if err := os.WriteFile(go18.File, []byte("name: "+go18.ID+"\ncommit: 0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(go19.File, []byte("name: "+go19.ID+"\ncommit: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, build, err = prepareStatus(recipes, man, prev, "5.1.0.11", exists, false)
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]string{}
	for _, entry := range doc.Recipes {
		got[entry.ID] = entry.State
	}
	if got[go18.ID] != stateCurrent || got[other.ID] != stateCurrent {
		t.Fatalf("go 1.18 and the unrelated recipe should stay current: %+v", got)
	}
	if got[go19.ID] != statePending || got[go21.ID] != statePending || len(build) != 2 {
		t.Fatalf("go 1.19 change should rebuild go 1.21 only: %+v build %d", got, len(build))
	}
}

func publishedManifest(recipes []recipe.Recipe) Manifest {
	man := emptyManifest()
	for _, r := range recipes {
		arches := map[string]ArchBuild{}
		for _, arch := range r.Arches {
			arches[arch] = ArchBuild{RPMs: []RPMRec{{
				File: r.ID + "/pkg." + arch + ".rpm",
				Name: "pkg",
			}}}
		}
		arches["src"] = ArchBuild{RPMs: []RPMRec{{
			File: r.ID + "/pkg.src.rpm",
			Name: "pkg",
		}}}
		man.Recipes[r.ID] = RecipeBuild{Commit: r.Commit, Arches: arches}
	}
	return man
}

func TestPrepareStatusKeepsSkippedError(t *testing.T) {
	r := testRecipe(t)
	fp, err := Fingerprint(r, "5.1.0.11")
	if err != nil {
		t.Fatal(err)
	}
	prev := Status{Recipes: []StatusRecipe{{
		ID:          r.ID,
		State:       stateSkipped,
		Fingerprint: fp,
		Error:       "patch failed",
		FailedAt:    "2026-02-03T04:05:06Z",
		Log:         "patch: hunk failed",
	}}}
	doc, build, err := prepareStatus([]recipe.Recipe{r}, emptyManifest(), prev, "5.1.0.11", func(string) (bool, error) {
		return false, nil
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(build) != 0 {
		t.Fatal("skipped failure must stay skipped")
	}
	if doc.Recipes[0].State != stateSkipped || doc.Recipes[0].Error != "patch failed" || doc.Recipes[0].FailedAt != "2026-02-03T04:05:06Z" {
		t.Fatalf("reason was not retained: %+v", doc.Recipes[0])
	}
}
