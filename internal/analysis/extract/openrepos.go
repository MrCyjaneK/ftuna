package extract

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
)

func openrepos(ctx context.Context, client *http.Client, root string) ([]Package, error) {
	doc, err := fetchInfo(ctx, client, root)
	if err != nil {
		return nil, err
	}
	if doc != nil {
		return openreposFromInfo(root, *doc)
	}
	log.Printf("openrepos: listing %s", root)
	dirs, err := listDirs(ctx, client, root)
	if err != nil {
		return nil, err
	}
	var out []Package
	for _, name := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir, err := joinDir(root, name)
		if err != nil {
			return nil, err
		}
		repo := dir + "personal/main/"
		log.Printf("openrepos: %s", name)
		pkgs, err := readRepo(ctx, client, repo)
		if err != nil {
			return nil, fmt.Errorf("openrepos %s: %w", name, err)
		}
		found, err := toPackages(repo, "mirror/openrepos/"+name+"/personal/main/", pkgs)
		if err != nil {
			return nil, fmt.Errorf("openrepos %s: %w", name, err)
		}
		log.Printf("openrepos: %s: %d packages", name, len(found))
		out = append(out, found...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no binary packages", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
