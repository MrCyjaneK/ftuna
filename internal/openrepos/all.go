package openrepos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

const (
	absentKey      = "meta/openrepos/absent.json"
	mirrorProbeGap = 100 * time.Millisecond
	absentFlush    = 50
)

type absentUser struct {
	Name      string `json:"name"`
	UID       string `json:"uid"`
	Status    int    `json:"status"`
	CheckedAt string `json:"checked_at"`
}

type absentFile struct {
	Users []absentUser `json:"users"`
}

type mirrorFailure struct {
	Name  string `json:"name"`
	UID   string `json:"uid"`
	Error string `json:"error"`
}

// MirrorAll checks every stored OpenRepos account. A 404 on the Sailfish
// repository is recorded and skipped on later runs. Accounts that publish
// a repository are mirrored.
func MirrorAll(ctx context.Context, store *s3store.Store, force bool) error {
	users, err := listUsers(ctx, store)
	if err != nil {
		return err
	}
	absent, err := loadAbsent(ctx, store)
	if err != nil {
		return err
	}
	log.Printf("checking %d accounts (%d already have no repository)", len(users), len(absent))

	var failures []mirrorFailure
	mirrored, skipped, freshAbsent := 0, 0, 0
	dirty := 0
	flush := func() error {
		if dirty == 0 {
			return nil
		}
		if err := saveAbsent(ctx, store, absent); err != nil {
			return err
		}
		dirty = 0
		return nil
	}
	defer func() { _ = flush() }()

	for i, user := range users {
		if err := ctx.Err(); err != nil {
			if ferr := flush(); ferr != nil {
				return ferr
			}
			return err
		}
		if !force {
			if _, ok := absent[user.Name]; ok {
				skipped++
				if (i+1)%200 == 0 || i == len(users)-1 {
					log.Printf("progress %d/%d mirrored %d absent %d skipped %d errors %d", i+1, len(users), mirrored, len(absent), skipped, len(failures))
				}
				continue
			}
		}
		if mirrored+freshAbsent+len(failures) > 0 {
			if err := sleep(ctx, mirrorProbeGap); err != nil {
				if ferr := flush(); ferr != nil {
					return ferr
				}
				return err
			}
		}
		err := mirrorPublisher(ctx, store, user, force)
		switch {
		case err == nil:
			mirrored++
			if _, ok := absent[user.Name]; ok {
				delete(absent, user.Name)
				dirty++
			}
		case errors.Is(err, ErrNoRepo):
			absent[user.Name] = absentUser{
				Name: user.Name, UID: user.UID, Status: 404,
				CheckedAt: time.Now().UTC().Format(time.RFC3339),
			}
			freshAbsent++
			dirty++
			if dirty >= absentFlush {
				if err := flush(); err != nil {
					return err
				}
			}
		default:
			failures = append(failures, mirrorFailure{Name: user.Name, UID: user.UID, Error: err.Error()})
			log.Printf("mirror %s: %v", user.Name, err)
		}
		if (i+1)%200 == 0 || i == len(users)-1 {
			log.Printf("progress %d/%d mirrored %d absent %d skipped %d errors %d", i+1, len(users), mirrored, len(absent), skipped, len(failures))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	log.Printf("finished %d accounts: mirrored %d, no repository %d, skipped %d, errors %d", len(users), mirrored, len(absent), skipped, len(failures))
	if err := PublishCatalog(ctx, store); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d repositories failed", len(failures))
	}
	return nil
}

func loadAbsent(ctx context.Context, store *s3store.Store) (map[string]absentUser, error) {
	body, ok, err := store.GetIfExists(ctx, absentKey)
	if err != nil || !ok {
		return map[string]absentUser{}, err
	}
	var doc absentFile
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", absentKey, err)
	}
	out := make(map[string]absentUser, len(doc.Users))
	for _, user := range doc.Users {
		out[user.Name] = user
	}
	return out, nil
}

func saveAbsent(ctx context.Context, store *s3store.Store, absent map[string]absentUser) error {
	users := make([]absentUser, 0, len(absent))
	for _, user := range absent {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	body, err := json.MarshalIndent(absentFile{Users: users}, "", "  ")
	if err != nil {
		return err
	}
	_, err = store.PutBytes(ctx, absentKey, "application/json", append(body, '\n'), nil)
	if err != nil {
		return err
	}
	log.Printf("stored %d accounts with no repository at s3://%s/%s", len(users), store.Bucket(), absentKey)
	return nil
}
