package extract

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func listDirs(ctx context.Context, client *http.Client, raw string) ([]string, error) {
	body, err := get(ctx, client, raw, maxMeta)
	if err != nil {
		return nil, err
	}
	names := dirNames(string(body))
	if len(names) == 0 {
		return nil, fmt.Errorf("%s: listed no directories", raw)
	}
	sort.Strings(names)
	return names, nil
}

func dirNames(body string) []string {
	if strings.Contains(body, `href="`) {
		return htmlDirs(body)
	}
	return textDirs(body)
}

func textDirs(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, " ") || !strings.HasSuffix(line, "/") {
			continue
		}
		name := strings.TrimSuffix(line, "/")
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func htmlDirs(body string) []string {
	var out []string
	seen := map[string]bool{}
	rest := body
	for {
		i := strings.Index(rest, `href="`)
		if i < 0 {
			return out
		}
		rest = rest[i+6:]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			return out
		}
		name := dirHref(html.UnescapeString(rest[:j]))
		rest = rest[j+1:]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
}

func dirHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.ContainsAny(href, "?#") {
		return ""
	}
	if strings.Contains(href, "://") {
		u, err := url.Parse(href)
		if err != nil {
			return ""
		}
		href = u.Path
	}
	href = strings.TrimPrefix(href, "./")
	if !strings.HasSuffix(href, "/") {
		return ""
	}
	href = strings.Trim(href, "/")
	if i := strings.LastIndex(href, "/"); i >= 0 {
		href = href[i+1:]
	}
	if href == "" || href == "." || href == ".." {
		return ""
	}
	return href
}
