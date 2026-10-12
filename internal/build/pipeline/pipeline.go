package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/mrcyjanek/ftuna/internal/build/checkout"
	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
	"github.com/mrcyjanek/ftuna/internal/build/rpmmd"
	"github.com/mrcyjanek/ftuna/internal/build/sfosbuild"
	"github.com/mrcyjanek/ftuna/internal/build/sign"
	"github.com/mrcyjanek/ftuna/internal/scratch"
	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

func All(ctx context.Context, store *s3store.Store, version string) error {
	recipesRoot, err := recipe.Root()
	if err != nil {
		return err
	}
	recipes, err := recipe.Discover(recipesRoot)
	if err != nil {
		return err
	}
	if len(recipes) == 0 {
		return fmt.Errorf("no recipes in %s", recipesRoot)
	}
	man, err := loadManifest(ctx, store, version)
	if err != nil {
		return err
	}
	prev, err := loadStatus(ctx, store, version)
	if err != nil {
		return err
	}
	exists := headExists(ctx, store)
	doc, build, err := prepareStatus(recipes, man, prev, version, exists)
	if err != nil {
		return err
	}
	for _, entry := range doc.Recipes {
		switch entry.State {
		case stateSkipped:
			log.Printf("skip %s unchanged failure", entry.ID)
		case stateCurrent:
			log.Printf("skip %s", entry.ID)
		}
	}
	log.Printf("status https://repo-oss.ftuna.eu/%s/status.json", version)
	if err := putStatus(ctx, store, version, &doc); err != nil {
		return err
	}
	removed := Removed(recipes, man)
	arches := unionArches(recipes)
	ready, err := RepoReady(version, arches, exists)
	if err != nil {
		return err
	}
	rewrite := map[string]bool{}
	for _, arch := range arches {
		body, ok, err := store.GetIfExists(ctx, ArchPrefix(version, arch)+"repodata/repomd.xml")
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if !PhoneReadableMetadata(body) {
			log.Printf("%s %s metadata is zstd; rebuilding so the phone can read it", version, arch)
			ready = false
			rewrite[arch] = true
		}
		if rpmmd.ListsSQLite(body) {
			log.Printf("%s %s metadata includes sqlite; rewriting gzip xml", version, arch)
			ready = false
			rewrite[arch] = true
		}
	}
	if len(build) == 0 && len(removed) == 0 && ready {
		log.Printf("already built %s", version)
		return publishOSSInfo(ctx, store, recipesRoot, version, recipes, man)
	}
	if _, err := sign.EntityFromEnv(); err != nil {
		return err
	}
	oldArches := manifestArches(man)
	before := map[string]map[string]bool{}
	for _, arch := range oldArches {
		before[arch] = archFiles(man, arch)
	}
	work, err := os.MkdirTemp("", "ftuna-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	repoRoot := filepath.Join(work, "repo")
	local := map[string]map[string]string{}
	index := map[string]int{}
	for i := range doc.Recipes {
		index[doc.Recipes[i].ID] = i
	}
	for _, r := range build {
		i := index[r.ID]
		doc.Recipes[i].State = stateBuilding
		if err := putStatus(ctx, store, version, &doc); err != nil {
			return err
		}
		info, buildLog, err := buildAndQuery(ctx, r, version, work)
		if err != nil {
			noteFailure(&doc, i, err, buildLog)
			if err := putStatus(ctx, store, version, &doc); err != nil {
				return err
			}
			continue
		}
		placed, err := placeBuilt(filepath.Join(work, "rpms"), info, r.ID, r.Arches, repoRoot)
		if err != nil {
			noteFailure(&doc, i, err, buildLog)
			if err := putStatus(ctx, store, version, &doc); err != nil {
				return err
			}
			continue
		}
		setRecipe(&man, r, placed)
		for arch, rpms := range placed {
			if local[arch] == nil {
				local[arch] = map[string]string{}
			}
			for _, rpm := range rpms {
				href := filepath.ToSlash(rpm.File)
				local[arch][href] = filepath.Join(repoRoot, arch, filepath.FromSlash(href))
			}
		}
		noteBuilt(&doc, i, r, placed)
		if err := putStatus(ctx, store, version, &doc); err != nil {
			return err
		}
	}
	for _, name := range removed {
		delete(man.Recipes, name)
	}
	entity, err := sign.EntityFromEnv()
	if err != nil {
		return err
	}
	for _, arch := range arches {
		want := archFiles(man, arch)
		if len(want) == 0 {
			log.Printf("%s %s has no rpms", version, arch)
			continue
		}
		changed := rewrite[arch] || len(local[arch]) > 0 || !sameFiles(before[arch], want)
		if !changed {
			ok, err := archPublished(ctx, store, version, arch)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
		}
		if err := publishArch(ctx, store, entity, version, arch, work, want, before[arch], local[arch]); err != nil {
			return err
		}
	}
	keep := map[string]bool{}
	for _, arch := range arches {
		keep[arch] = true
	}
	for _, arch := range oldArches {
		if keep[arch] {
			continue
		}
		if err := deletePrefix(ctx, store, ArchPrefix(version, arch)); err != nil {
			return err
		}
	}
	pub, err := sign.PublicArmor(entity)
	if err != nil {
		return err
	}
	if _, err := store.PutBytes(ctx, PublicKey, "application/pgp-keys", []byte(pub), nil); err != nil {
		return err
	}
	if err := putManifest(ctx, store, version, man); err != nil {
		return err
	}
	if err := putStatus(ctx, store, version, &doc); err != nil {
		return err
	}
	return publishOSSInfo(ctx, store, recipesRoot, version, recipes, man)
}

func One(ctx context.Context, version, name, id string) error {
	if err := scratch.CheckName(id); err != nil {
		return err
	}
	if _, err := scratch.LoadToken(); err != nil {
		return fmt.Errorf("%w; run ftuna scratch register", err)
	}
	recipesRoot, err := recipe.Root()
	if err != nil {
		return err
	}
	recipes, err := recipe.Discover(recipesRoot)
	if err != nil {
		return err
	}
	var r recipe.Recipe
	found := false
	for _, item := range recipes {
		if item.ID == name {
			r = item
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown recipe %s", name)
	}
	work, err := os.MkdirTemp("", "ftuna-one-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	repoRoot := filepath.Join(work, "repo")
	info, _, err := buildAndQuery(ctx, r, version, work)
	if err != nil {
		return fmt.Errorf("%s: %w", r.ID, err)
	}
	byArch, err := placeBuilt(filepath.Join(work, "rpms"), info, r.ID, r.Arches, repoRoot)
	if err != nil {
		return fmt.Errorf("%s: %w", r.ID, err)
	}
	for _, arch := range r.Arches {
		if err := rpmmd.Write(filepath.Join(repoRoot, arch)); err != nil {
			return err
		}
	}
	token, err := scratch.LoadToken()
	if err != nil {
		return fmt.Errorf("%w; run ftuna scratch register", err)
	}
	account, err := scratch.Account(token)
	if err != nil {
		return err
	}
	if err := writeScratchInfo(recipesRoot, repoRoot, account, id, r, byArch); err != nil {
		return err
	}
	if err := checkScratchTree(repoRoot); err != nil {
		return err
	}
	repo, err := scratch.Publish(ctx, scratch.BaseURL(), token, repoRoot, id)
	if err != nil {
		return err
	}
	log.Printf("scratch %s%s", scratch.BaseURL(), repo)
	return nil
}

func buildAndQuery(ctx context.Context, r recipe.Recipe, version, work string) ([]deps.RPM, string, error) {
	// Basename becomes the sfosbuild image name. A shared "src" would let the next
	// recipe rebuild that image and drop libraries compiled by image hooks.
	src := filepath.Join(work, strings.ReplaceAll(r.ID, "/", "_"))
	if err := os.RemoveAll(src); err != nil {
		return nil, "", err
	}
	if err := checkout.Prepare(ctx, r.URL, r.Commit, r.Patches, r.Overlay, src); err != nil {
		return nil, "", err
	}
	if len(r.Repos) > 0 {
		log.Printf("repos %s", strings.Join(r.Repos, " "))
		if err := recipe.InstallRepos(src, r.Repos); err != nil {
			return nil, "", err
		}
		if err := checkout.CommitPath(ctx, src, ".sfosbuild/image/00-ftuna-repos.sh", "ftuna repos"); err != nil {
			return nil, "", err
		}
	}
	out := filepath.Join(work, "rpms")
	if err := os.RemoveAll(out); err != nil {
		return nil, "", err
	}
	if r.Path == "." {
		log.Printf("sfosbuild %s %s %s", version, recipe.ArchArg(r.Arches), r.ID)
	} else {
		log.Printf("sfosbuild %s %s %s %s", version, recipe.ArchArg(r.Arches), r.ID, r.Path)
	}
	buildLog, err := sfosbuild.Run(ctx, version, recipe.ArchArg(r.Arches), src, out, r.Path)
	if err != nil {
		return nil, buildLog, err
	}
	paths, err := listRPMs(out)
	if err != nil {
		return nil, buildLog, err
	}
	outRPMs := make([]deps.RPM, 0, len(paths))
	for _, path := range paths {
		rpm, err := rpmmd.Read(path)
		if err != nil {
			return nil, buildLog, err
		}
		outRPMs = append(outRPMs, rpm)
	}
	return outRPMs, buildLog, nil
}

func headExists(ctx context.Context, store *s3store.Store) func(string) (bool, error) {
	return func(key string) (bool, error) {
		_, ok, err := store.Head(ctx, key)
		return ok, err
	}
}

func loadManifest(ctx context.Context, store *s3store.Store, version string) (Manifest, error) {
	body, ok, err := store.GetIfExists(ctx, ManifestKey(version))
	if err != nil {
		return Manifest{}, err
	}
	if !ok {
		return emptyManifest(), nil
	}
	var man Manifest
	if err := json.Unmarshal(body, &man); err != nil {
		return Manifest{}, fmt.Errorf("manifest %s: %w", version, err)
	}
	if man.Recipes == nil {
		man.Recipes = map[string]RecipeBuild{}
	}
	return man, nil
}

func putManifest(ctx context.Context, store *s3store.Store, version string, man Manifest) error {
	if man.Recipes == nil {
		man.Recipes = map[string]RecipeBuild{}
	}
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = store.PutBytes(ctx, ManifestKey(version), "application/json", raw, nil)
	return err
}

func setRecipe(man *Manifest, r recipe.Recipe, byArch map[string][]deps.RPM) {
	if man.Recipes == nil {
		man.Recipes = map[string]RecipeBuild{}
	}
	rb := RecipeBuild{Commit: r.Commit, Repos: append([]string{}, r.Repos...), Arches: map[string]ArchBuild{}}
	for _, arch := range r.Arches {
		var recs []RPMRec
		for _, rpm := range byArch[arch] {
			recs = append(recs, fromDeps(rpm))
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].File < recs[j].File })
		rb.Arches[arch] = ArchBuild{RPMs: recs}
	}
	man.Recipes[r.ID] = rb
}

func archFiles(man Manifest, arch string) map[string]bool {
	out := map[string]bool{}
	for _, rb := range man.Recipes {
		for _, rpm := range rb.Arches[arch].RPMs {
			if rpm.File != "" {
				out[filepath.ToSlash(rpm.File)] = true
			}
		}
	}
	return out
}

func sameFiles(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

func archPublished(ctx context.Context, store *s3store.Store, version, arch string) (bool, error) {
	prefix := ArchPrefix(version, arch)
	for _, rel := range []string{"repodata/repomd.xml", "repodata/repomd.xml.asc", "repomd.xml.asc"} {
		_, ok, err := store.Head(ctx, prefix+rel)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func publishArch(ctx context.Context, store *s3store.Store, entity *openpgp.Entity, version, arch, work string, want, old map[string]bool, local map[string]string) error {
	log.Printf("repodata %s %s", version, arch)
	cache, err := rpmmd.CacheDir(version, arch)
	if err != nil {
		return err
	}
	pkgs, err := assemblePackages(ctx, store, version, arch, work, cache, want, local)
	if err != nil {
		return err
	}
	if err := rpmmd.Prune(cache, want); err != nil {
		return err
	}
	meta := filepath.Join(work, "meta-"+arch)
	if err := os.MkdirAll(meta, 0o755); err != nil {
		return err
	}
	if err := rpmmd.WritePackages(meta, pkgs); err != nil {
		return err
	}
	repomd := filepath.Join(meta, "repodata", "repomd.xml")
	body, err := os.ReadFile(repomd)
	if err != nil {
		return err
	}
	sig, err := sign.Sign(entity, body)
	if err != nil {
		return err
	}
	if err := os.WriteFile(repomd+".asc", []byte(sig), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(meta, "repomd.xml.asc"), []byte(sig), 0o644); err != nil {
		return err
	}
	prefix := ArchPrefix(version, arch)
	uploaded := map[string]bool{}
	for href, path := range local {
		key := prefix + href
		if _, err := store.PutFile(ctx, key, contentType(path), path, nil); err != nil {
			return err
		}
		uploaded[key] = true
	}
	if err := uploadDir(ctx, store, prefix, meta, uploaded); err != nil {
		return err
	}
	for href := range old {
		if want[href] {
			continue
		}
		if err := store.Delete(ctx, prefix+href); err != nil {
			return err
		}
	}
	keys, err := store.List(ctx, prefix+"repodata/")
	if err != nil {
		return err
	}
	for _, key := range keys {
		if uploaded[key] {
			continue
		}
		if err := store.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func assemblePackages(ctx context.Context, store *s3store.Store, version, arch, work, cache string, want map[string]bool, local map[string]string) ([]rpmmd.Package, error) {
	var pkgs []rpmmd.Package
	var missing []string
	for href := range want {
		if path, ok := local[href]; ok {
			pkg, err := rpmmd.Inspect(path, href)
			if err != nil {
				return nil, err
			}
			if err := rpmmd.SavePackage(cache, pkg); err != nil {
				return nil, err
			}
			pkgs = append(pkgs, pkg)
			continue
		}
		pkg, ok, err := rpmmd.LoadPackage(cache, href)
		if err != nil {
			return nil, err
		}
		if ok {
			pkgs = append(pkgs, pkg)
			continue
		}
		missing = append(missing, href)
	}
	if len(missing) == 0 {
		return pkgs, nil
	}
	fromXML, err := packagesFromRepo(ctx, store, version, arch)
	if err != nil {
		return nil, err
	}
	var still []string
	for _, href := range missing {
		pkg, ok := fromXML[href]
		if !ok {
			still = append(still, href)
			continue
		}
		if err := rpmmd.SavePackage(cache, pkg); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	if len(still) == 0 {
		return pkgs, nil
	}
	log.Printf("%s %s reading %d rpms", version, arch, len(still))
	dir := filepath.Join(work, "fetch-"+arch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	prefix := ArchPrefix(version, arch)
	for _, href := range still {
		dest := filepath.Join(dir, strings.ReplaceAll(href, "/", "_"))
		if err := store.GetFile(ctx, prefix+href, dest); err != nil {
			return nil, err
		}
		pkg, err := rpmmd.Inspect(dest, href)
		if err != nil {
			return nil, err
		}
		if err := rpmmd.SavePackage(cache, pkg); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func packagesFromRepo(ctx context.Context, store *s3store.Store, version, arch string) (map[string]rpmmd.Package, error) {
	prefix := ArchPrefix(version, arch)
	body, ok, err := store.GetIfExists(ctx, prefix+"repodata/repomd.xml")
	if err != nil || !ok {
		return nil, err
	}
	primary, lists, other, err := rpmmd.GzipHrefs(body)
	if err != nil {
		return nil, nil
	}
	log.Printf("%s %s metadata from published repodata", version, arch)
	plain := make([][]byte, 3)
	for i, href := range []string{primary, lists, other} {
		raw, err := store.Get(ctx, prefix+href)
		if err != nil {
			return nil, err
		}
		plain[i], err = rpmmd.Gunzip(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", href, err)
		}
	}
	return rpmmd.Split(plain[0], plain[1], plain[2])
}

func placeBuilt(outDir string, info []deps.RPM, recipeName string, arches []string, repoRoot string) (map[string][]deps.RPM, error) {
	want := map[string]bool{}
	for _, arch := range arches {
		want[arch] = true
		if err := os.MkdirAll(filepath.Join(repoRoot, arch), 0o755); err != nil {
			return nil, err
		}
	}
	got := map[string][]deps.RPM{}
	for _, rpm := range info {
		arch, err := ArchOf(rpm.File)
		if err != nil {
			return nil, err
		}
		src := filepath.Join(outDir, filepath.Base(rpm.File))
		if _, err := os.Stat(src); err != nil {
			return nil, fmt.Errorf("rpm %s: %w", rpm.File, err)
		}
		targets := []string{arch}
		if arch == "noarch" {
			targets = arches
		} else if !want[arch] {
			return nil, fmt.Errorf("unexpected arch %s in %s", arch, rpm.File)
		}
		rel := recipeName + "/" + filepath.Base(rpm.File)
		placed := rpm
		placed.File = rel
		for _, arch := range targets {
			if err := copyFile(src, filepath.Join(repoRoot, arch, filepath.FromSlash(rel))); err != nil {
				return nil, err
			}
			got[arch] = append(got[arch], placed)
		}
	}
	for _, arch := range arches {
		if len(got[arch]) == 0 {
			return nil, fmt.Errorf("no rpms for %s", arch)
		}
	}
	return got, nil
}

func listRPMs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rpm") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no rpms in %s", dir)
	}
	return out, nil
}

func uploadDir(ctx context.Context, store *s3store.Store, prefix, dir string, uploaded map[string]bool) error {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		ri, rj := rank(files[i]), rank(files[j])
		if ri != rj {
			return ri < rj
		}
		return files[i] < files[j]
	})
	for _, path := range files {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		key := prefix + filepath.ToSlash(rel)
		if _, err := store.PutFile(ctx, key, contentType(path), path, nil); err != nil {
			return err
		}
		uploaded[key] = true
	}
	return nil
}

func deletePrefix(ctx context.Context, store *s3store.Store, prefix string) error {
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := store.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func rank(path string) int {
	slash := filepath.ToSlash(path)
	if strings.HasSuffix(slash, "repomd.xml.asc") {
		return 2
	}
	if strings.Contains(slash, "repodata/") {
		return 1
	}
	return 0
}

func contentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".rpm"):
		return "application/x-rpm"
	case strings.HasSuffix(path, ".asc"):
		return "application/pgp-signature"
	case strings.HasSuffix(path, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(path, ".xml"):
		return "text/xml"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
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

var (
	scratchSeg  = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
	scratchMeta = regexp.MustCompile(`^[0-9a-fA-F]+-(primary|filelists|other)\.xml\.gz$`)
)

func checkScratchTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("scratch: symlink %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !scratchPathOK(rel) {
			return fmt.Errorf("scratch: %s is not allowed in the archive", rel)
		}
		return nil
	})
}

func scratchPathOK(name string) bool {
	if name == "info.json.xz" {
		return true
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 || (parts[0] != "aarch64" && parts[0] != "armv7hl" && parts[0] != "i486") {
		return false
	}
	if parts[1] == "repodata" {
		if len(parts) != 3 {
			return false
		}
		file := parts[2]
		if file == "repomd.xml" || file == "repomd.xml.asc" {
			return true
		}
		return scratchMeta.MatchString(file)
	}
	if !strings.HasSuffix(name, ".rpm") || !scratchSeg.MatchString(parts[len(parts)-1]) {
		return false
	}
	for _, part := range parts[1 : len(parts)-1] {
		if !scratchSeg.MatchString(part) {
			return false
		}
	}
	return true
}
