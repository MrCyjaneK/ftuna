package scan

import (
	"fmt"
	"strings"

	"github.com/mrcyjanek/ftuna/internal/analysis/extract"
)

// Config is the scan invocation. Sources default to chum, openrepos, and oss.
// RatingsDir is where result files are stored. It is required.
// CacheDir is the RPM byte cache. Empty uses $XDG_CACHE_HOME/ftuna/rpmcache,
// or ~/.cache/ftuna/rpmcache. Entries are kept until something else deletes them.
// Chum, OpenRepos, and OSS override those sources' index URLs.
type Config struct {
	Sources    []string
	RatingsDir string
	CacheDir   string
	Chum       string
	OpenRepos  string
	OSS        string
}

// ParseArgs reads the ratings directory and optional source names.
// The directory is required. With no source names, every source is selected.
func ParseArgs(args []string) (Config, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Config{}, fmt.Errorf("usage: ftuna scan <dir> [chum|openrepos|oss]...")
	}
	cfg := Config{RatingsDir: args[0]}
	seen := map[string]bool{}
	for _, arg := range args[1:] {
		switch arg {
		case extract.Chum, extract.OpenRepos, extract.OSS:
			if seen[arg] {
				continue
			}
			seen[arg] = true
			cfg.Sources = append(cfg.Sources, arg)
		case "":
			return Config{}, fmt.Errorf("unexpected argument")
		default:
			if arg[0] == '-' {
				return Config{}, fmt.Errorf("unknown flag %s", arg)
			}
			return Config{}, fmt.Errorf("unexpected argument %s", arg)
		}
	}
	if len(cfg.Sources) == 0 {
		cfg.Sources = []string{extract.Chum, extract.OpenRepos, extract.OSS}
	}
	return cfg, nil
}

func (c Config) root(name string) string {
	switch name {
	case extract.Chum:
		return c.Chum
	case extract.OpenRepos:
		return c.OpenRepos
	case extract.OSS:
		return c.OSS
	default:
		return ""
	}
}
