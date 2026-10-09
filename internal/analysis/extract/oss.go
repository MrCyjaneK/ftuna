package extract

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
)

var versionName = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`)

func oss(ctx context.Context, client *http.Client, root string) ([]Package, error) {
	sums, err := readInfo(ctx, client, root)
	if err != nil {
		return nil, err
	}
	log.Printf("oss: listing %s", root)
	dirs, err := listDirs(ctx, client, root)
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, name := range dirs {
		if versionName.MatchString(name) {
			versions = append(versions, name)
		}
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("%s: listed no oss releases", root)
	}
	var out []Package
	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		verURL, err := joinDir(root, version)
		if err != nil {
			return nil, err
		}
		arches, err := listDirs(ctx, client, verURL)
		if err != nil {
			return nil, fmt.Errorf("oss %s: %w", version, err)
		}
		var found []Package
		for _, arch := range arches {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			repo, err := joinDir(verURL, arch)
			if err != nil {
				return nil, err
			}
			log.Printf("oss: %s/%s", version, arch)
			pkgs, err := readRepo(ctx, client, repo)
			if errors.Is(err, errNotFound) {
				log.Printf("oss: %s/%s has no repomd.xml", version, arch)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("oss %s/%s: %w", version, arch, err)
			}
			batch, err := toPackages(repo, "repo/ftuna-oss/"+version+"/"+arch+"/", pkgs)
			if err != nil {
				return nil, fmt.Errorf("oss %s/%s: %w", version, arch, err)
			}
			log.Printf("oss: %s/%s: %d packages", version, arch, len(batch))
			found = append(found, batch...)
		}
		if len(found) == 0 {
			return nil, fmt.Errorf("oss: %s listed no binary packages", version)
		}
		out = append(out, found...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return overlayInfo(sums, out)
}
