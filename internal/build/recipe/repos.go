package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const repoHook = ".sfosbuild/image/00-ftuna-repos.sh"

var repoURL = regexp.MustCompile(`^https://[A-Za-z0-9._~:/?#\[\]@!$&()*+,=%{}-]+$`)

func cleanRepos(recipeName string, repos []string) ([]string, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(repos))
	for _, raw := range repos {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("%s: repos contains an empty URL", recipeName)
		}
		repo, key, err := splitRepo(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", recipeName, err)
		}
		if err := checkRepoURL(recipeName, "repo URL", repo); err != nil {
			return nil, err
		}
		if key != "" {
			if err := checkRepoURL(recipeName, "signing key", key); err != nil {
				return nil, err
			}
		}
		if seen[repo] {
			return nil, fmt.Errorf("%s: duplicate repo URL %s", recipeName, repo)
		}
		seen[repo] = true
		out = append(out, raw)
	}
	return out, nil
}

// splitRepo separates an optional signing key: https://repo;https://key.
func splitRepo(raw string) (repo, key string, err error) {
	repo, key, found := strings.Cut(raw, ";")
	if !found {
		return raw, "", nil
	}
	if repo == "" || key == "" || strings.Contains(key, ";") {
		return "", "", fmt.Errorf("repo URL %q must be https://repo or https://repo;https://key", raw)
	}
	return repo, key, nil
}

func checkRepoURL(recipeName, kind, raw string) error {
	if !repoURL.MatchString(raw) {
		return fmt.Errorf("%s: %s %q must be an https URL", recipeName, kind, raw)
	}
	host := strings.TrimPrefix(raw, "https://")
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if host == "" || strings.Contains(host, "{") {
		return fmt.Errorf("%s: %s %q needs a host", recipeName, kind, raw)
	}
	if err := checkPlaceholders(raw); err != nil {
		return fmt.Errorf("%s: %s: %w", recipeName, kind, err)
	}
	return nil
}

func checkPlaceholders(raw string) error {
	rest := raw
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			if strings.Contains(rest, "}") {
				return fmt.Errorf("%s has an unmatched }", raw)
			}
			return nil
		}
		rest = rest[i+1:]
		j := strings.IndexByte(rest, '}')
		if j < 0 {
			return fmt.Errorf("%s has an unmatched {", raw)
		}
		switch rest[:j] {
		case "version", "arch", "release":
		default:
			return fmt.Errorf("%s has unknown placeholder {%s}; use {version}, {release}, or {arch}", raw, rest[:j])
		}
		rest = rest[j+1:]
	}
}

// InstallRepos writes the image hook that adds repos before the package is built.
// An empty list writes nothing. {version} is the full Sailfish OS version.
// {release} is major.minor. {arch} is the architecture of that image.
// A {version}_{arch} URL follows Chum and uses major.minor from 4.6 onward.
// A repo may be https://repo;https://key. The key is the repository gpgkey.
func InstallRepos(checkout string, repos []string) error {
	if len(repos) == 0 {
		return nil
	}
	path := filepath.Join(checkout, filepath.FromSlash(repoHook))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(repoScript(repos)), 0o644)
}

func repoScript(repos []string) string {
	var b strings.Builder
	b.WriteString(`#!/bin/sh
# Recipe repos, installed before the other image hooks.
# {version} is the Sailfish OS version (5.1.0.11).
# {release} is major.minor (5.1).
# {arch} is aarch64, armv7hl, or i486.
# {version}_{arch} follows SailfishOS:Chum: from 4.6, {version} is major.minor.
# REPO;KEY puts KEY in gpgkey= so zypper can import it while refreshing.
set -eu
version=${SFOS_VERSION:?}
arch=${SFOS_ARCH:?}
release=$(printf '%s\n' "$version" | cut -d. -f1,2)

chum_version() {
	case "$1" in
	4.[6-9]*|[5-9]*|[1-9][0-9]*)
		printf '%s\n' "$1" | cut -d. -f1,2
		;;
	*)
		printf '%s\n' "$1"
		;;
	esac
}

expand() {
	ver=$version
	case "$1" in
	*'{version}_{arch}'*)
		ver=$(chum_version "$version")
		;;
	esac
	printf '%s\n' "$1" | sed \
		-e "s|{version}|${ver}|g" \
		-e "s|{release}|${release}|g" \
		-e "s|{arch}|${arch}|g"
}

n=0
while IFS= read -r pattern; do
	[ -n "$pattern" ] || continue
	repo=$pattern
	key=
	case "$pattern" in
	*';'*)
		repo=${pattern%%;*}
		key=${pattern#*;}
		;;
	esac
	url=$(expand "$repo")
	n=$((n + 1))
	name="ftuna-extra-${n}"
	if [ -n "$key" ]; then
		keyurl=$(expand "$key")
		cat > "/tmp/${name}.repo" << EOF
[${name}]
name=${name}
enabled=1
autorefresh=1
baseurl=${url}
type=rpm-md
gpgcheck=1
gpgkey=${keyurl}
EOF
		zypper --non-interactive ar "/tmp/${name}.repo"
	else
		zypper --non-interactive ar -f "$url" "$name"
	fi
	zypper --non-interactive --gpg-auto-import-keys refresh "$name"
done <<'EOF'
`)
	for _, repo := range repos {
		b.WriteString(repo)
		b.WriteByte('\n')
	}
	b.WriteString("EOF\n")
	return b.String()
}
