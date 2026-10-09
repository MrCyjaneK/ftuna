package extract

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// releaseName matches a Chum directory: 4.4.0.64_aarch64 or 5.1_aarch64.
var releaseName = regexp.MustCompile(`^(?:[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+|[0-9]+\.[0-9]+)_(?:aarch64|armv7hl|i486)$`)

func chum(ctx context.Context, client *http.Client, root string) ([]Package, error) {
	log.Printf("chum: listing %s", root)
	dirs, err := listDirs(ctx, client, root)
	if err != nil {
		return nil, err
	}
	var releases []string
	for _, name := range dirs {
		if releaseName.MatchString(name) && releaseCurrent(name) {
			releases = append(releases, name)
		}
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("%s: listed no chum repositories at 5.1 or newer", root)
	}
	var out []Package
	for _, name := range releases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		repo, err := joinDir(root, name)
		if err != nil {
			return nil, err
		}
		log.Printf("chum: %s", name)
		pkgs, err := readRepo(ctx, client, repo)
		if err != nil {
			return nil, fmt.Errorf("chum %s: %w", name, err)
		}
		found, err := toPackages(repo, "chum/"+name+"/", pkgs)
		if err != nil {
			return nil, fmt.Errorf("chum %s: %w", name, err)
		}
		log.Printf("chum: %s: %d packages", name, len(found))
		out = append(out, found...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no binary packages", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// releaseCurrent reports whether a Chum directory is Sailfish OS 5.1 or newer.
// The version is the part before the architecture: 5.1_aarch64, 5.2_armv7hl,
// or 4.4.0.64_i486.
func releaseCurrent(name string) bool {
	ver, _, ok := strings.Cut(name, "_")
	if !ok {
		return false
	}
	majorText, minorText, ok := strings.Cut(ver, ".")
	if !ok {
		return false
	}
	minorText, _, _ = strings.Cut(minorText, ".")
	major, err1 := strconv.Atoi(majorText)
	minor, err2 := strconv.Atoi(minorText)
	if err1 != nil || err2 != nil {
		return false
	}
	if major != 5 {
		return major > 5
	}
	return minor >= 1
}
