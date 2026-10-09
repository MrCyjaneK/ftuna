package openrepos

import (
	"fmt"
	"path"
	"strings"
)

// safeRel accepts a repository-relative href and returns it unchanged when it
// is already a clean relative path. OpenRepos locations are stored as those
// exact relative paths; cleaning or rewriting them would be a different tree.
func safeRel(href string) (string, error) {
	if href == "" {
		return "", fmt.Errorf("empty location")
	}
	if strings.ContainsRune(href, 0) || strings.ContainsRune(href, '\\') {
		return "", fmt.Errorf("location %q contains a forbidden character", href)
	}
	if strings.Contains(href, "://") || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "?") || strings.ContainsAny(href, "?#") {
		return "", fmt.Errorf("location %q is not a relative repository path", href)
	}
	if path.Clean(href) != href || href == "." || href == ".." || strings.HasPrefix(href, "../") {
		return "", fmt.Errorf("location %q is not a clean relative path", href)
	}
	return href, nil
}

func publisherPrefix(name string) string {
	return "mirror/openrepos/" + name + "/"
}

func repoKey(publisher, rel string) string {
	return publisherPrefix(publisher) + "personal/main/" + rel
}

func repoFileKey(publisher string) string {
	return publisherPrefix(publisher) + "personal-main.repo"
}
