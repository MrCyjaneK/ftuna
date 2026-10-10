package openrepos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const (
	appsURL      = "https://openrepos.net/api/v1/apps"
	appsPageSize = 100
	// OpenRepos omits a few rows inside a page, so a page can be shorter
	// than appsPageSize while later pages still hold applications.
	// Stopping on the first short page dropped everything after it,
	// including Advanced Camera. An empty page is the end of the list.
	appsMaxPages = 200
	appsPrefix   = "cdn-mirror/openrepos.net/apps/"
	assetHost    = "openrepos.net"
	assetRoot    = "/sites/default/files/"
	cdnHost      = "cdn-mirror.ftuna.eu"
	cdnRoot      = "cdn-mirror/openrepos.net"
	maxAPIBody   = 8 << 20
	maxAsset     = 32 << 20
	catalogGets  = 32
)

var (
	errNotFound     = errors.New("not found")
	errAppCurrent   = errors.New("current")
	errAppAbsent    = errors.New("absent")
	errAssetMissing = errors.New("missing asset")
)

// appBackup is one OpenRepos application stored on the CDN.
// Description keeps the original HTML. Icon and screenshots are CDN URLs.
type appBackup struct {
	AppID       string   `json:"appid"`
	Updated     string   `json:"updated"`
	Title       string   `json:"title"`
	UID         string   `json:"uid"`
	User        string   `json:"user"`
	Package     string   `json:"package"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Screenshots []string `json:"screenshots"`
}

type appListItem struct {
	AppID     flexString `json:"appid"`
	Updated   flexString `json:"updated"`
	Published flexString `json:"published"`
}

type appDetail struct {
	AppID       flexString `json:"appid"`
	Title       looseText  `json:"title"`
	Updated     flexString `json:"updated"`
	Published   flexString `json:"published"`
	Body        looseText  `json:"body"`
	User        appUser    `json:"user"`
	Package     appPackage `json:"package"`
	Icon        appIcon    `json:"icon"`
	Screenshots appShots   `json:"screenshots"`
}

type appUser struct {
	UID  flexString `json:"uid"`
	Name looseText  `json:"name"`
}

type appPackage struct {
	Name looseText `json:"name"`
}

type appIcon struct {
	URL looseText `json:"url"`
}

type appShot struct {
	URL looseText `json:"url"`
}

func (s *appShot) UnmarshalJSON(b []byte) error {
	if emptyJSON(b) {
		*s = appShot{}
		return nil
	}
	type plain appShot
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = appShot(v)
	return nil
}

type appShots []appShot

// looseText reads a JSON string or number. OpenRepos sends false when a
// text field is empty, and null for a missing object.
type looseText string

func (s *looseText) UnmarshalJSON(b []byte) error {
	b = bytesTrim(b)
	switch string(b) {
	case "", "null", "false", "true":
		*s = ""
		return nil
	}
	var v flexString
	if err := v.UnmarshalJSON(b); err != nil {
		return err
	}
	*s = looseText(v)
	return nil
}

func (s *appUser) UnmarshalJSON(b []byte) error {
	if emptyJSON(b) {
		*s = appUser{}
		return nil
	}
	type plain appUser
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = appUser(v)
	return nil
}

func (s *appPackage) UnmarshalJSON(b []byte) error {
	if emptyJSON(b) {
		*s = appPackage{}
		return nil
	}
	type plain appPackage
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = appPackage(v)
	return nil
}

func (s *appIcon) UnmarshalJSON(b []byte) error {
	if emptyJSON(b) {
		*s = appIcon{}
		return nil
	}
	type plain appIcon
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = appIcon(v)
	return nil
}

func (s *appShots) UnmarshalJSON(b []byte) error {
	if emptyJSON(b) {
		*s = nil
		return nil
	}
	var v []appShot
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = v
	return nil
}

func emptyJSON(b []byte) bool {
	switch string(bytesTrim(b)) {
	case "", "null", "false", "true":
		return true
	default:
		return false
	}
}

// flexString reads a JSON string or integer. The apps list sends numeric ids
// and the application record sends decimal strings.
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	b = bytesTrim(b)
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = flexString(v)
		return nil
	}
	if !numericID(string(b)) {
		return fmt.Errorf("invalid number %s", b)
	}
	*s = flexString(b)
	return nil
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func numericID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Extract copies each OpenRepos application's description, icon, and
// screenshots onto the CDN, then writes info.json.xz from that backup.
// An application whose updated time is unchanged is left in place unless
// force is set.
func Extract(ctx context.Context, store *s3store.Store, force bool) error {
	client := httpClient()
	heads, err := listApps(ctx, client)
	if err != nil {
		return err
	}
	var failures []string
	stored, skipped := 0, 0
	for i, head := range heads {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			if err := sleep(ctx, mirrorProbeGap); err != nil {
				return err
			}
		}
		err := extractOne(ctx, store, client, head, force)
		switch {
		case errors.Is(err, errAppCurrent), errors.Is(err, errAppAbsent):
			skipped++
		case err == nil:
			stored++
		default:
			failures = append(failures, string(head.AppID))
			log.Printf("extract %s: %v", head.AppID, err)
		}
		if (i+1)%50 == 0 || i == len(heads)-1 {
			log.Printf("extract %d/%d stored %d skipped %d errors %d", i+1, len(heads), stored, skipped, len(failures))
		}
	}
	if err := PublishCatalog(ctx, store); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d applications failed", len(failures))
	}
	return nil
}

func listApps(ctx context.Context, client *http.Client) ([]appListItem, error) {
	return listAppPages(ctx, func(page int) ([]byte, error) {
		rawURL := fmt.Sprintf("%s?page=%d&pagesize=%d", appsURL, page, appsPageSize)
		return apiBytes(ctx, client, rawURL)
	})
}

func listAppPages(ctx context.Context, fetch func(page int) ([]byte, error)) ([]appListItem, error) {
	var items []appListItem
	seen := map[string]bool{}
	ended := false
	for page := 0; page < appsMaxPages; page++ {
		if page > 0 {
			if err := sleep(ctx, mirrorProbeGap); err != nil {
				return nil, err
			}
		}
		body, err := fetch(page)
		if err != nil {
			return nil, err
		}
		var pageItems []appListItem
		if err := json.Unmarshal(body, &pageItems); err != nil {
			return nil, fmt.Errorf("apps page %d: %w", page, err)
		}
		if len(pageItems) == 0 {
			ended = true
			break
		}
		fresh := 0
		added := 0
		for _, item := range pageItems {
			id := string(item.AppID)
			if !numericID(id) {
				return nil, fmt.Errorf("apps page %d: bad app id %q", page, id)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			fresh++
			if pub := string(item.Published); pub != "" && pub != "1" {
				continue
			}
			items = append(items, item)
			added++
		}
		log.Printf("apps page %d: %d new (%d total)", page, added, len(items))
		if fresh == 0 {
			return nil, fmt.Errorf("apps page %d repeated records already stored", page)
		}
	}
	if !ended {
		return nil, fmt.Errorf("apps list exceeded %d pages", appsMaxPages)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("openrepos returned no applications; refusing to continue")
	}
	return items, nil
}

func extractOne(ctx context.Context, store *s3store.Store, client *http.Client, head appListItem, force bool) error {
	id := string(head.AppID)
	prev, ok, err := readBackup(ctx, store, id)
	if err != nil {
		return err
	}
	if ok && !force && prev.Updated == string(head.Updated) {
		return errAppCurrent
	}
	detail, err := fetchApp(ctx, client, id)
	if errors.Is(err, errNotFound) {
		log.Printf("extract %s: application not found", id)
		return errAppAbsent
	}
	if err != nil {
		return err
	}
	if pub := string(detail.Published); pub != "" && pub != "1" {
		log.Printf("extract %s: unpublished", id)
		return errAppAbsent
	}
	overwrite := force || (ok && prev.Updated != string(head.Updated))
	icon, err := mirrorAsset(ctx, store, client, string(detail.Icon.URL), overwrite)
	if errors.Is(err, errAssetMissing) {
		log.Printf("extract %s: missing icon %s", id, detail.Icon.URL)
		icon = ""
	} else if err != nil {
		return err
	}
	shots := []string{}
	for _, shot := range detail.Screenshots {
		public, err := mirrorAsset(ctx, store, client, string(shot.URL), overwrite)
		if errors.Is(err, errAssetMissing) {
			log.Printf("extract %s: missing screenshot %s", id, shot.URL)
			continue
		}
		if err != nil {
			return err
		}
		if public != "" {
			shots = append(shots, public)
		}
	}
	updated := string(head.Updated)
	if updated == "" {
		updated = string(detail.Updated)
	}
	return saveBackup(ctx, store, appBackup{
		AppID:       id,
		Updated:     updated,
		Title:       strings.TrimSpace(string(detail.Title)),
		UID:         string(detail.User.UID),
		User:        strings.TrimSpace(string(detail.User.Name)),
		Package:     strings.TrimSpace(string(detail.Package.Name)),
		Description: string(detail.Body),
		Icon:        icon,
		Screenshots: shots,
	})
}

func fetchApp(ctx context.Context, client *http.Client, id string) (appDetail, error) {
	body, err := apiBytes(ctx, client, appsURL+"/"+id)
	if err != nil {
		return appDetail{}, err
	}
	var doc appDetail
	if err := json.Unmarshal(body, &doc); err != nil {
		return appDetail{}, fmt.Errorf("app %s: %w", id, err)
	}
	if string(doc.AppID) != id {
		return appDetail{}, fmt.Errorf("app %s: response id %q", id, doc.AppID)
	}
	return doc, nil
}

func apiBytes(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	extra := make(http.Header)
	extra.Set("Warehouse-Platform", "SailfishOS")
	extra.Set("Accept", "application/json")
	resp, err := get(ctx, client, rawURL, extra)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAPIBody {
		return nil, fmt.Errorf("%s: response larger than %d bytes", rawURL, maxAPIBody)
	}
	return body, nil
}

func mirrorAsset(ctx context.Context, store *s3store.Store, client *http.Client, raw string, overwrite bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	origin, err := assetPath(raw)
	if err != nil {
		return "", err
	}
	key := assetKey(origin)
	public := assetPublic(origin)
	if !overwrite {
		if _, ok, err := store.Head(ctx, key); err != nil {
			return "", err
		} else if ok {
			return public, nil
		}
	}
	resp, err := get(ctx, client, raw, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", errAssetMissing
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", raw, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAsset+1))
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", fmt.Errorf("%s: empty body", raw)
	}
	if len(body) > maxAsset {
		return "", fmt.Errorf("%s: larger than %d bytes", raw, maxAsset)
	}
	if _, err := store.PutBytes(ctx, key, assetType(origin), body, nil); err != nil {
		return "", err
	}
	return public, nil
}

func assetPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme != "https" || u.Host != assetHost {
		return "", fmt.Errorf("%q must be https://%s%s...", raw, assetHost, assetRoot)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q has extra URL parts", raw)
	}
	p := u.Path
	if !strings.HasPrefix(p, assetRoot) || strings.Contains(p, "\\") || strings.Contains(p, "\x00") {
		return "", fmt.Errorf("%q has an invalid path", raw)
	}
	if path.Clean(p) != p || strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") {
		return "", fmt.Errorf("%q is not a clean path", raw)
	}
	return p, nil
}

func assetKey(originPath string) string {
	return cdnRoot + originPath
}

func assetPublic(originPath string) string {
	parts := strings.Split(strings.TrimPrefix(originPath, "/"), "/")
	esc := make([]string, len(parts))
	for i, part := range parts {
		esc[i] = url.PathEscape(part)
	}
	return "https://" + cdnHost + "/openrepos.net/" + strings.Join(esc, "/")
}

func assetType(originPath string) string {
	switch strings.ToLower(path.Ext(originPath)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func appObjectKey(id string) string {
	return appsPrefix + id + ".json"
}

func readBackup(ctx context.Context, store *s3store.Store, id string) (appBackup, bool, error) {
	body, ok, err := store.GetIfExists(ctx, appObjectKey(id))
	if err != nil || !ok {
		return appBackup{}, false, err
	}
	var item appBackup
	if err := json.Unmarshal(body, &item); err != nil {
		return appBackup{}, false, nil
	}
	return item, true, nil
}

func saveBackup(ctx context.Context, store *s3store.Store, item appBackup) error {
	if item.Screenshots == nil {
		item.Screenshots = []string{}
	}
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = store.PutBytes(ctx, appObjectKey(item.AppID), "application/json", body, nil)
	return err
}

func loadAppIndex(ctx context.Context, store *s3store.Store) (map[string]appBackup, error) {
	keys, err := store.ListNotify(ctx, appsPrefix, func(n int) {
		log.Printf("catalog list %s %d", appsPrefix, n)
	})
	if err != nil {
		return nil, err
	}
	log.Printf("catalog %s", usersKey)
	names, err := publisherNames(ctx, store)
	if err != nil {
		return nil, err
	}
	var jobs []string
	for _, key := range keys {
		if appIDFromKey(key) != "" {
			jobs = append(jobs, key)
		}
	}
	backups, err := fetchAppBackups(ctx, store, jobs)
	if err != nil {
		return nil, err
	}
	log.Printf("catalog read %d openrepos apps", len(backups))
	return indexApps(backups, names), nil
}

func fetchAppBackups(ctx context.Context, store *s3store.Store, keys []string) ([]appBackup, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	slot := make([]appBackup, len(keys))
	ok := make([]bool, len(keys))
	var next atomic.Int64
	var mu sync.Mutex
	var first error
	n := min(catalogGets, len(keys))
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(keys) {
					return
				}
				if err := ctx.Err(); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
					return
				}
				item, found, err := readAppBackup(ctx, store, keys[i])
				if err != nil {
					mu.Lock()
					if first == nil || (errors.Is(first, context.Canceled) && !errors.Is(err, context.Canceled)) {
						first = err
						cancel()
					}
					mu.Unlock()
					return
				}
				if found {
					slot[i], ok[i] = item, true
				}
			}
		}()
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	backups := make([]appBackup, 0, len(keys))
	for i := range slot {
		if ok[i] {
			backups = append(backups, slot[i])
		}
	}
	return backups, nil
}

func readAppBackup(ctx context.Context, store *s3store.Store, key string) (appBackup, bool, error) {
	log.Printf("catalog %s", key)
	body, exists, err := store.GetIfExists(ctx, key)
	if err != nil || !exists {
		return appBackup{}, false, err
	}
	var item appBackup
	if err := json.Unmarshal(body, &item); err != nil {
		return appBackup{}, false, fmt.Errorf("parse %s: %w", key, err)
	}
	return item, true, nil
}

func appIDFromKey(key string) string {
	if !strings.HasPrefix(key, appsPrefix) || !strings.HasSuffix(key, ".json") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(key, appsPrefix), ".json")
	if strings.Contains(id, "/") || !numericID(id) {
		return ""
	}
	return id
}

func publisherNames(ctx context.Context, store *s3store.Store) (map[string]string, error) {
	body, ok, err := store.GetIfExists(ctx, usersKey)
	if err != nil || !ok {
		return map[string]string{}, err
	}
	var doc usersFile
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", usersKey, err)
	}
	out := make(map[string]string, len(doc.Users))
	for _, raw := range doc.Users {
		var u userRecord
		if err := json.Unmarshal(raw, &u); err != nil {
			return nil, fmt.Errorf("parse %s: %w", usersKey, err)
		}
		if u.UID != "" && u.Name != "" {
			out[u.UID] = u.Name
		}
	}
	return out, nil
}

func indexApps(backups []appBackup, uidToName map[string]string) map[string]appBackup {
	out := make(map[string]appBackup, len(backups))
	for _, item := range backups {
		publisher := uidToName[item.UID]
		if publisher == "" {
			publisher = item.User
		}
		if validPublisher(publisher) != nil || item.Package == "" {
			continue
		}
		key := appIndexKey(publisher, item.Package)
		if prev, ok := out[key]; ok && !newerStamp(item.Updated, prev.Updated) {
			continue
		}
		out[key] = item
	}
	return out
}

func appIndexKey(publisher, pkg string) string {
	return publisher + "\n" + strings.ToLower(pkg)
}

func newerStamp(a, b string) bool {
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil {
		return ai > bi
	}
	return a > b
}

func matchApp(apps map[string]appBackup, publisher string, rows []pkgRef) (appBackup, bool) {
	if len(apps) == 0 {
		return appBackup{}, false
	}
	var found appBackup
	var ok bool
	for _, row := range rows {
		item, hit := apps[appIndexKey(publisher, row.Name)]
		if !hit {
			continue
		}
		if installable(row.Name) {
			return item, true
		}
		if !ok {
			found, ok = item, true
		}
	}
	return found, ok
}

func cdnImage(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != cdnHost || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/openrepos.net"+assetRoot) {
		return ""
	}
	if path.Clean(u.Path) != u.Path {
		return ""
	}
	return raw
}

func plainDescription(raw string) string {
	var b strings.Builder
	i := 0
	for i < len(raw) {
		if raw[i] != '<' {
			b.WriteByte(raw[i])
			i++
			continue
		}
		end := strings.IndexByte(raw[i:], '>')
		if end < 0 {
			b.WriteString(raw[i:])
			break
		}
		name := tagName(raw[i+1 : i+end])
		i += end + 1
		switch name {
		case "br", "/p", "/div", "/tr", "/h1", "/h2", "/h3", "/h4", "/li", "/ul", "/ol", "/blockquote":
			b.WriteByte('\n')
		case "li":
			if b.Len() == 0 || !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
		}
	}
	text := strings.ReplaceAll(html.UnescapeString(b.String()), "\u00a0", " ")
	return normalizeLines(text)
}

func tagName(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" || strings.HasPrefix(tag, "!") {
		return ""
	}
	closing := false
	if strings.HasPrefix(tag, "/") {
		closing = true
		tag = strings.TrimSpace(tag[1:])
	}
	fields := strings.Fields(tag)
	if len(fields) == 0 {
		return ""
	}
	name := strings.ToLower(strings.TrimRight(fields[0], "/"))
	if closing {
		return "/" + name
	}
	return name
}

func normalizeLines(s string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if blank || len(out) == 0 {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
