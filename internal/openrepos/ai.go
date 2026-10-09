package openrepos

import (
	"regexp"
	"strings"
)

var (
	chumMetaLine = regexp.MustCompile(`^(Title|Type|DeveloperName|PackagedBy|Categories|AIRating|AINote|AINoteMD|Custom|PackageIcon|Screenshots|Links|PackageName|Icon|PackagerName|Url):`)
	aiRatingLine = regexp.MustCompile(`(?m)^AIRating:[ \t]*([^\n]*)`)
	blankLines   = regexp.MustCompile(`\n[ \t]*\n`)
)

// chumAI splits a trailing Chum metadata paragraph from an RPM description and
// reads AIRating from it. A missing paragraph or an unrecognized rating leaves
// code empty, which the catalog badge renders as the unknown image.
func chumAI(description string) (body, code string) {
	description = strings.ReplaceAll(description, "\r\n", "\n")
	description = strings.TrimRight(description, "\n")
	parts := paragraphs(description)
	if len(parts) == 0 {
		return "", ""
	}
	last := parts[len(parts)-1]
	if !isChumMeta(last) {
		return description, ""
	}
	body = strings.TrimRight(strings.Join(parts[:len(parts)-1], "\n\n"), "\n")
	return body, ratingFrom(last)
}

func paragraphs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	raw := blankLines.Split(s, -1)
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.Trim(part, "\n")
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func isChumMeta(paragraph string) bool {
	for _, line := range strings.Split(paragraph, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return chumMetaLine.MatchString(line)
	}
	return false
}

func ratingFrom(paragraph string) string {
	m := aiRatingLine.FindStringSubmatch(paragraph)
	if m == nil {
		return ""
	}
	return normalizeAI(m[1])
}

func normalizeAI(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		raw = raw[:i]
	}
	switch strings.ToUpper(raw) {
	case "H", "A", "B", "C", "V", "X":
		return strings.ToUpper(raw)
	default:
		return ""
	}
}
