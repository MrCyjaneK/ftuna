package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrcyjanek/ftuna/internal/analysis/check"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/cron"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/dbus"
	"github.com/mrcyjanek/ftuna/internal/analysis/extract"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/filecaps"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/hooks"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/polkit"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/sailjail"
	"github.com/mrcyjanek/ftuna/internal/analysis/scan"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/scriptlet"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/setuid"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/systemd"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/udev"
	_ "github.com/mrcyjanek/ftuna/internal/analysis/worldwrite"
	"github.com/mrcyjanek/ftuna/internal/build/pipeline"
	"github.com/mrcyjanek/ftuna/internal/build/recipe"
	"github.com/mrcyjanek/ftuna/internal/build/sign"
	"github.com/mrcyjanek/ftuna/internal/openrepos"
	"github.com/mrcyjanek/ftuna/internal/scratch"
	"github.com/mrcyjanek/ftuna/internal/store/envfile"
	"github.com/mrcyjanek/ftuna/internal/store/s3store"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("ftuna: ")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := envfile.Load(filepath.Join(wd, ".env")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "openrepos":
		return runOpenRepos(ctx, args)
	case "scan":
		return runScan(ctx, args[1:])
	case "chum", "oss":
		return runScan(ctx, args)
	case "build":
		return runBuild(ctx, args[1:])
	case "scratch":
		return runScratch(ctx, args[1:])
	default:
		if args[0] == "gc" || recipe.ValidVersion(args[0]) {
			return runBuild(ctx, args)
		}
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runScratch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return fmt.Errorf("scratch needs a subcommand")
	}
	base := scratch.BaseURL()
	switch args[0] {
	case "register":
		if len(args) != 1 {
			return fmt.Errorf("usage: ftuna scratch register")
		}
		token, err := scratch.LoadToken()
		if err == nil {
			account, err := scratch.Account(token)
			if err != nil {
				return err
			}
			ok, err := scratch.Registered(ctx, base, token)
			if err != nil {
				return err
			}
			if ok {
				fmt.Println("Already registered")
				fmt.Printf("%s/%s/\n", base, account)
				return nil
			}
			fmt.Printf("Saved account %s is no longer on %s\n", account, base)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("Registering an account on %s\n", base)
		challenge, bits, err := scratch.Challenge(ctx, base)
		if err != nil {
			return err
		}
		var hashes atomic.Uint64
		done := make(chan struct{})
		go func() {
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				n := hashes.Load()
				if n == 0 {
					return
				}
				left := math.Ldexp(1, bits) / (float64(n) / 2)
				eta := time.Duration(left * float64(time.Second)).Round(time.Second)
				if eta < time.Second {
					eta = time.Second
				}
				fmt.Printf("About %s left\n", eta)
			}
		}()
		solution, err := scratch.Solve(ctx, challenge, bits, &hashes)
		close(done)
		if err != nil {
			return err
		}
		account, token, err := scratch.Submit(ctx, base, challenge, solution)
		if err != nil {
			return err
		}
		path, err := scratch.SaveToken(token)
		if err != nil {
			return err
		}
		fmt.Printf("Token saved to %s\n", path)
		fmt.Printf("%s/%s/\n", base, account)
		return nil
	case "publish":
		if len(args) != 3 {
			return fmt.Errorf("usage: ftuna scratch publish <dir> <name>")
		}
		token, err := scratch.LoadToken()
		if err != nil {
			return err
		}
		repo, err := scratch.Publish(ctx, base, token, args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Println(base + repo)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runBuild(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage(os.Stdout)
		return nil
	}
	version := args[0]
	if !recipe.ValidVersion(version) {
		return fmt.Errorf("sfos version must look like 5.1.0.11, got %q", version)
	}
	force, _, rest, err := splitArgs(args[1:], false)
	if err != nil {
		return err
	}
	if force {
		return fmt.Errorf("--force does not apply")
	}
	if len(rest) == 1 && rest[0] == "all" {
		store, err := openStore(true)
		if err != nil {
			return err
		}
		return pipeline.All(ctx, store, version)
	}
	if len(rest) != 2 {
		usage(os.Stderr)
		return fmt.Errorf("usage: ftuna <sfos-version> all | ftuna <sfos-version> <recipe> <name>")
	}
	if err := scratch.CheckName(rest[1]); err != nil {
		return err
	}
	if _, err := scratch.LoadToken(); err != nil {
		return fmt.Errorf("%w; run ftuna scratch register", err)
	}
	return pipeline.One(ctx, version, rest[0], rest[1])
}

func runOpenRepos(ctx context.Context, args []string) error {
	if len(args) < 2 {
		usage(os.Stderr)
		return fmt.Errorf("openrepos needs a subcommand")
	}
	store, err := openStore(false)
	if err != nil {
		return err
	}
	switch args[1] {
	case "catalog":
		if len(args) != 2 {
			return fmt.Errorf("usage: ftuna openrepos catalog")
		}
		return openrepos.PublishCatalog(ctx, store)
	case "extract":
		force, _, rest, err := splitArgs(args[2:], false)
		if err != nil {
			return err
		}
		if len(rest) != 0 {
			return fmt.Errorf("usage: ftuna openrepos extract [--force]")
		}
		return openrepos.Extract(ctx, store, force)
	case "users":
		force, _, rest, err := splitArgs(args[2:], false)
		if err != nil {
			return err
		}
		if len(rest) != 0 {
			return fmt.Errorf("usage: ftuna openrepos users [--force]")
		}
		return openrepos.SyncUsers(ctx, store, force)
	case "mirror":
		force, all, rest, err := splitArgs(args[2:], true)
		if err != nil {
			return err
		}
		if all {
			if len(rest) != 0 {
				return fmt.Errorf("usage: ftuna openrepos mirror --all [--force]")
			}
			return openrepos.MirrorAll(ctx, store, force)
		}
		if len(rest) != 1 {
			return fmt.Errorf("usage: ftuna openrepos mirror <publisher> [--force]")
		}
		return openrepos.Mirror(ctx, store, rest[0], force)
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown openrepos command %q", args[1])
	}
}

func runScan(ctx context.Context, args []string) error {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			usage(os.Stdout)
			return nil
		}
	}
	cfg, err := scan.ParseArgs(args)
	if err != nil {
		usage(os.Stderr)
		return err
	}
	return scan.Run(ctx, cfg)
}

func openStore(signing bool) (*s3store.Store, error) {
	if signing {
		if _, err := sign.EntityFromEnv(); err != nil {
			return nil, err
		}
	}
	return s3store.FromEnv()
}

// splitArgs accepts --force, and --all when allowAll is set.
func splitArgs(args []string, allowAll bool) (force, all bool, rest []string, err error) {
	for _, arg := range args {
		switch arg {
		case "--force":
			force = true
		case "--all":
			if !allowAll {
				return false, false, nil, fmt.Errorf("unknown flag %s", arg)
			}
			all = true
		default:
			if strings.HasPrefix(arg, "-") {
				return false, false, nil, fmt.Errorf("unknown flag %s", arg)
			}
			rest = append(rest, arg)
		}
	}
	return force, all, rest, nil
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `Usage:
  ftuna <sfos-version> all
  ftuna <sfos-version> <recipe> <name>
  ftuna gc
  ftuna openrepos users [--force]
  ftuna openrepos mirror <publisher> [--force]
  ftuna openrepos mirror --all [--force]
  ftuna openrepos extract [--force]
  ftuna openrepos catalog
  ftuna scan <dir> [chum|openrepos|oss]...
  ftuna scratch register
  ftuna scratch publish <dir> <name>

all       Build every recipe for that Sailfish OS version and publish
          repo/ftuna-oss/<version>/<arch>/. Recipes already stored for
          the same commit are not built again.
<recipe> <name>
          Build one recipe and publish the RPMs it produced at
          scratchpad-repo.ftuna.eu/<account>/<name>/. <name> is the
          directory under the account, 1-80 characters of a-z, 0-9, _,
          or -. Publishing the same name again replaces that repo, so a
          phone that added it keeps receiving updates. The repo expires
          after 14 days. This fails unless a scratch token is already
          saved.

scratch register
          Register an account on scratchpad-repo.ftuna.eu. If a saved
          token still matches an account there, this exits 0 and prints
          the account URL. A token whose account is gone starts
          registration again. The proof of work takes about a minute.
          The API token is saved to $HOME/.config/ftuna/scratch-token
          (mode 0600).
scratch publish <dir> <name>
          Pack <dir> as an uncompressed ustar archive and upload it.
          RPM files are placed under their architecture and repodata is
          written first. A directory that already has repodata/repomd.xml
          for each architecture is uploaded unchanged. The public URL is
          printed. Repos expire 14 days after the last publish.
          <name> is required. It is the directory under the account,
          1-80 characters of a-z, 0-9, _, or -. Publishing the same name
          again replaces that repo, so a phone that added it keeps
          receiving updates.

Recipes are read from FTUNA_RECIPES.
The public repomd.xml is signed with FTUNA_OSS_GPG_PRIVATE_KEY from .env
(base64 of an armored secret key with an empty passphrase).

users    Download the OpenRepos user list and store it on S3.
         A list less than 24 hours old is kept. --force downloads it again.
mirror   Copy one Sailfish publisher repository onto S3, byte for byte.
         An unchanged repomd.xml is left alone. --force checks every file.
         --all checks every account. A 404 is stored and skipped next time.
         mirror and mirror --all then write the catalog.
extract  Copy each application's description, icon, and screenshots onto
         the CDN. An unchanged updated time is left in place. --force
         downloads every application again. extract then writes the catalog.
catalog  Write mirror/openrepos/info.json.xz from the stored mirrors and
         the CDN application backup.

scan     Read each source's rpm-md and write
         <dir>/<aa>/<bb>/<cc>/<sha256>.json for each binary RPM that is not
         already stored at version %d.
         OpenRepos takes its package list from info.json.xz. With no
         source names, chum, openrepos, and oss are all read.
           chum        %s
           openrepos   %s
           oss         %s

State is written to S3. Credentials come from .env in the working directory
or the environment:
S3_ENDPOINT, S3_BUCKET_NAME, S3_REGION, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY.
FTUNA_RECIPES is the recipes checkout.
`, check.Version, extract.ChumRoot, extract.OpenReposRoot, extract.OSSRoot)
}
