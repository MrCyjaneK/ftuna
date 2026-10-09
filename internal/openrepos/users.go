package openrepos

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const (
	usersURL     = "https://openrepos.net/api/v1/users"
	usersKey     = "meta/openrepos/users.json"
	usersPerPage = 5000
	// The users API sends Cache-Control: no-cache and no validator, so a
	// conditional GET cannot succeed. A full scan is three page requests.
	// Repeat it at most once a day.
	usersRefresh = 24 * time.Hour
	usersPageGap = time.Second
)

type userRecord struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

type usersFile struct {
	FetchedAt string            `json:"fetched_at"`
	Source    string            `json:"source"`
	Count     int               `json:"count"`
	Users     []json.RawMessage `json:"users"`
}

// SyncUsers downloads every OpenRepos account and stores the list on S3.
// A list newer than usersRefresh is left in place unless force is set.
func SyncUsers(ctx context.Context, store *s3store.Store, force bool) error {
	if !force {
		if skip, err := usersAreCurrent(ctx, store); err != nil {
			return err
		} else if skip {
			return nil
		}
	}
	client := httpClient()
	byUID := map[string]json.RawMessage{}
	for page := 1; ; page++ {
		if page > 1 {
			if err := sleep(ctx, usersPageGap); err != nil {
				return err
			}
		}
		url := fmt.Sprintf("%s?page=%d&pagesize=%d", usersURL, page, usersPerPage)
		body, err := downloadBytes(ctx, client, url)
		if err != nil {
			return err
		}
		var raw []json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return fmt.Errorf("users page %d: %w", page, err)
		}
		if len(raw) == 0 {
			break
		}
		added := 0
		for _, item := range raw {
			var head userRecord
			if err := json.Unmarshal(item, &head); err != nil {
				return fmt.Errorf("users page %d: %w", page, err)
			}
			if head.UID == "" {
				return fmt.Errorf("users page %d: entry without uid", page)
			}
			if _, ok := byUID[head.UID]; ok {
				continue
			}
			byUID[head.UID] = item
			added++
		}
		log.Printf("users page %d: %d new (%d total)", page, added, len(byUID))
		if added == 0 {
			return fmt.Errorf("users page %d repeated records already stored", page)
		}
		if len(raw) < usersPerPage {
			break
		}
	}
	if len(byUID) == 0 {
		return fmt.Errorf("openrepos returned no users; refusing to replace %s", usersKey)
	}

	uids := make([]string, 0, len(byUID))
	for uid := range byUID {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool {
		ai, aerr := strconv.ParseInt(uids[i], 10, 64)
		bi, berr := strconv.ParseInt(uids[j], 10, 64)
		if aerr == nil && berr == nil && ai != bi {
			return ai < bi
		}
		return uids[i] < uids[j]
	})
	users := make([]json.RawMessage, 0, len(uids))
	for _, uid := range uids {
		users = append(users, byUID[uid])
	}
	doc := usersFile{
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Source:    usersURL,
		Count:     len(users),
		Users:     users,
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if _, err := store.PutBytes(ctx, usersKey, "application/json", body, nil); err != nil {
		return err
	}
	log.Printf("stored %d users at s3://%s/%s", len(users), store.Bucket(), usersKey)
	return nil
}

func usersAreCurrent(ctx context.Context, store *s3store.Store) (bool, error) {
	body, ok, err := store.GetIfExists(ctx, usersKey)
	if err != nil || !ok {
		return false, err
	}
	var doc usersFile
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, nil
	}
	if !usersCurrent(doc.FetchedAt, time.Now()) {
		return false, nil
	}
	log.Printf("users list has %d accounts fetched at %s; next refresh after %s (pass --force to refresh now)", doc.Count, doc.FetchedAt, usersRefresh)
	return true, nil
}

func usersCurrent(fetchedAt string, now time.Time) bool {
	fetched, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return false
	}
	age := now.Sub(fetched)
	return age >= 0 && age < usersRefresh
}

func loadUsers(ctx context.Context, store *s3store.Store) (map[string]userRecord, error) {
	body, err := store.Get(ctx, usersKey)
	if err != nil {
		return nil, fmt.Errorf("read %s (%w); run: ftuna openrepos users", usersKey, err)
	}
	var doc usersFile
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", usersKey, err)
	}
	byName := make(map[string]userRecord, len(doc.Users))
	for _, raw := range doc.Users {
		var u userRecord
		if err := json.Unmarshal(raw, &u); err != nil {
			return nil, err
		}
		if u.Name == "" {
			continue
		}
		if prev, ok := byName[u.Name]; ok && prev.UID != u.UID {
			return nil, fmt.Errorf("publisher name %q is used by uid %s and uid %s", u.Name, prev.UID, u.UID)
		}
		byName[u.Name] = u
	}
	return byName, nil
}

func listUsers(ctx context.Context, store *s3store.Store) ([]userRecord, error) {
	byName, err := loadUsers(ctx, store)
	if err != nil {
		return nil, err
	}
	users := make([]userRecord, 0, len(byName))
	for _, user := range byName {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	return users, nil
}

// requirePublisher confirms the Sailfish repository owner is in the stored user list.
func requirePublisher(ctx context.Context, store *s3store.Store, name string) (userRecord, error) {
	users, err := loadUsers(ctx, store)
	if err != nil {
		return userRecord{}, err
	}
	u, ok := users[name]
	if !ok {
		return userRecord{}, fmt.Errorf("publisher %q is not in %s", name, usersKey)
	}
	return u, nil
}
