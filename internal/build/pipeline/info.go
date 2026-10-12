package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/build/catalog"
	"github.com/mrcyjanek/ftuna/internal/build/cdn"
	"github.com/mrcyjanek/ftuna/internal/build/deps"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

func publishOSSInfo(ctx context.Context, store *s3store.Store, root, version string, recipes []recipe.Recipe, man Manifest) error {
	items, err := ossItems(ctx, store, root, version, recipes, man)
	if err != nil {
		return err
	}
	return putInfo(ctx, store, PublicRoot+"info.json.xz", "https://repo-oss.ftuna.eu/info.json.xz", items)
}

func writeScratchInfo(root, repoRoot, account, id string, r recipe.Recipe, byArch map[string][]deps.RPM) error {
	items, err := scratchItems(root, repoRoot, account, id, r, byArch)
	if err != nil {
		return err
	}
	body, err := catalog.Encode(catalog.Assemble(items))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "info.json.xz"), body, 0o644); err != nil {
		return err
	}
	log.Printf("info https://scratchpad-repo.ftuna.eu/%s/%s/info.json.xz", account, id)
	return nil
}

func putInfo(ctx context.Context, store *s3store.Store, key, public string, items []catalog.Item) error {
	body, err := catalog.Encode(catalog.Assemble(items))
	if err != nil {
		return err
	}
	if _, err := store.PutBytes(ctx, key, "application/x-xz", body, nil); err != nil {
		return err
	}
	log.Printf("info %s", public)
	return nil
}

func ossItems(ctx context.Context, store *s3store.Store, root, version string, recipes []recipe.Recipe, man Manifest) ([]catalog.Item, error) {
	var items []catalog.Item
	for _, r := range recipes {
		rb, ok := man.Recipes[r.ID]
		if !ok {
			continue
		}
		var built []catalog.Built
		for _, arch := range r.Arches {
			recs := rb.Arches[arch].RPMs
			if len(recs) == 0 {
				continue
			}
			rpms := make([]deps.RPM, 0, len(recs))
			for _, rec := range recs {
				rpm := toDeps(rec)
				if err := fillSHA256(ctx, store, ArchPrefix(version, arch)+rpm.File, &rpm); err != nil {
					return nil, err
				}
				rpms = append(rpms, rpm)
			}
			built = append(built, catalog.Built{
				Arch:    arch,
				RepoID:  "ftuna-oss-" + version + "-" + arch,
				RepoURL: "https://repo-oss.ftuna.eu/" + version + "/" + arch + "/",
				RPMs:    rpms,
			})
		}
		if len(built) == 0 {
			continue
		}
		item, err := itemFor(ctx, store, root, r, built)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func scratchItems(root, repoRoot, account, id string, r recipe.Recipe, byArch map[string][]deps.RPM) ([]catalog.Item, error) {
	var built []catalog.Built
	for _, arch := range r.Arches {
		rpms := append([]deps.RPM{}, byArch[arch]...)
		if len(rpms) == 0 {
			continue
		}
		for i := range rpms {
			if rpms[i].SHA256 != "" {
				continue
			}
			sum, err := fileSHA256(filepath.Join(repoRoot, arch, filepath.FromSlash(rpms[i].File)))
			if err != nil {
				return nil, err
			}
			rpms[i].SHA256 = sum
		}
		built = append(built, catalog.Built{
			Arch:    arch,
			RepoID:  "scratch-" + account + "-" + id + "-" + arch,
			RepoURL: "https://scratchpad-repo.ftuna.eu/" + account + "/" + id + "/" + arch + "/",
			RPMs:    rpms,
		})
	}
	if len(built) == 0 {
		return nil, fmt.Errorf("%s: no rpms for info.json.xz", r.ID)
	}
	updated, err := recipe.Updated(root, r.File)
	if err != nil {
		return nil, err
	}
	return []catalog.Item{{
		Recipe:      r,
		Updated:     updated,
		Icon:        r.Icon,
		Screenshots: append([]string{}, r.Screenshots...),
		Built:       built,
	}}, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fillSHA256(ctx context.Context, store *s3store.Store, key string, rpm *deps.RPM) error {
	if rpm.SHA256 != "" {
		return nil
	}
	head, ok, err := store.Head(ctx, key)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("missing %s", key)
	}
	if head.SHA256 == "" {
		return fmt.Errorf("%s has no sha256 metadata", key)
	}
	rpm.SHA256 = strings.ToLower(head.SHA256)
	return nil
}

func itemFor(ctx context.Context, store *s3store.Store, root string, r recipe.Recipe, built []catalog.Built) (catalog.Item, error) {
	updated, err := recipe.Updated(root, r.File)
	if err != nil {
		return catalog.Item{}, err
	}
	icon, shots, err := mirrorRecipe(ctx, store, r)
	if err != nil {
		return catalog.Item{}, fmt.Errorf("%s: %w", r.ID, err)
	}
	return catalog.Item{
		Recipe:      r,
		Updated:     updated,
		Icon:        icon,
		Screenshots: shots,
		Built:       built,
	}, nil
}

func mirrorRecipe(ctx context.Context, store *s3store.Store, r recipe.Recipe) (string, []string, error) {
	icon := ""
	if r.Icon != "" {
		public, err := cdn.Ensure(ctx, store, r.Icon)
		if err != nil {
			return "", nil, err
		}
		icon = public
	}
	shots := make([]string, 0, len(r.Screenshots))
	for _, raw := range r.Screenshots {
		public, err := cdn.Ensure(ctx, store, raw)
		if err != nil {
			return "", nil, err
		}
		shots = append(shots, public)
	}
	return icon, shots, nil
}
