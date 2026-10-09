# FTuna

> I miss F-Droid here a bit

## What?

FTuna is a Sailfish OS repository client and software catalog in one. You can add any standard rpm repository (Like Chum), use OpenRepos or FTuna build repo.

## FTuna Client

Simple, very minimal app that is a frontend to .rpm repositories. Chum is read from upstream. FTuna has its own repository, and OpenRepos is a mirror.

FTuna doesn't replace any repositories in particular but it aims to be a one-stop-shop for all repository needs. FTuna provides it's own repository (currently as an experiment) and also expands the listings with extra metadata, such as analysis of .rpm files to see what actually is inside (similar-ish/wannabe fdroid analysis).

## FTuna repo

FTuna hosts multiple repositories

### repo-oss.ftuna.eu

Open Source builds of all packages (soon to be added `repo-oss-b` to look for reproducibility issues...).
Builds are performed offline and natively (no i486 cross builds) using [sfosbuild](https://github.com/mrcyjanek/sfosbuild).

Some software does need external dependencies or special setup - sfosbuild offers image setup that can install 3rd party packages into the build repository and has internet access. This is however clearly visible to user in package rating.

### scratchpad-repo.ftuna.eu

Scratchpad is special - you can open pull request to ftuna repo and it will **automagically** create a temporary repo with your package - repo that expires after 14 days (since last update) and is marginally better than just throwing .rpm at your testers.

Maintainers of other software are encouraged to use CI/webhooks to maintain their own scratchpad releases. `ftuna scratch publish <dir> <name>` publishes to `/<account>/<name>/` (for example `/<account>/github_mrcyjanek_sfos_mullvad_pr_213/`) and replaces that directory on the next publish, so a phone that added the repo keeps updating. The name is 1-80 characters of `a-z`, `0-9`, `_`, or `-`.

### repo-openrepos.ftuna.eu

Optional opt-in repository that serves OpenRepos packages, signed by openrepos.net key. In addition metadata .json file is made available to add descriptions, metadata and other details. (Note: I wouldn't have made that mirror - as I don't necessarily enjoy increasing my S3 bill, but openrepos.net seems to ban all VPN providers :/)

## Storage

For backend we use S3, the server does absolutelny nothing but serving static files.

## S3 schema

```text
mirror/openrepos/...                               # upstream paths, unchanged
mirror/openrepos/<publisher>/personal-main.repo    # unchanged; still points at OpenRepos
mirror/openrepos/openrepos.key
mirror/openrepos/info.json.xz                      # catalog index, written after each mirror

repo/ftuna-oss/<version>/<arch>/<recipe>/*.rpm     # signed public rpm-md repo; repodata stays on <arch>/
repo/ftuna-oss/<version>/manifest.json             # commit -> rpm names, for rebuild skips
repo/ftuna-oss/info.json.xz                        # catalog index, regenerated on every all publish
repo/ftuna-oss/ftuna-oss.key
scratch/<user>/<id>/<arch>/<recipe>/*.rpm          # unsigned repo; <id> is the publish name
scratch/<user>/<id>/meta.json                      # created and expire, unix timestamps
scratch/<user>/<id>/info.json.xz                   # catalog index for that scratch repo
cdn-mirror/raw.githubusercontent.com/...           # icon and screenshot bytes
cdn-mirror/openrepos.net/apps/<id>.json            # OpenRepos description, icon, and screenshot URLs
cdn-mirror/openrepos.net/sites/default/files/...   # those icon and screenshot bytes
```

## Install

```text
go install github.com/mrcyjanek/ftuna/cmd/ftuna@latest
```

## OpenRepos mirror

```text
go run ./cmd/ftuna openrepos mirror --all
go run ./cmd/ftuna openrepos extract
go run ./cmd/ftuna openrepos catalog
```

`extract` copies each application's description, icon, and screenshots onto `cdn-mirror.ftuna.eu` and then rebuilds the catalog. An application is skipped when its `updated` time already matches the backup. `catalog` rebuilds `info.json.xz` from the publisher mirrors and that CDN backup. `mirror` and `mirror --all` write the catalog after they finish.

## Chum

The client reads SailfishOS:Chum from `https://repo.sailfishos.org/obs/sailfishos:/chum/`. From Sailfish OS 4.6 that is the `major.minor` directory the Chum app uses, such as `5.1_aarch64`. Older releases use the full version.

Enabling Chum adds that repository for installation. If the Chum app already added `sailfishos-chum` or `sailfishos-chum-testing`, FTuna uses that repository. It does not add a second one, and it does not disable the Chum app's repository.

## Build

Recipes live in [ftuna-recipes](https://github.com/MrCyjaneK/ftuna-recipes). `FTUNA_RECIPES` is that checkout. Each recipe is `<git-host>/<owner>/<name>/` with a pinned `build.yaml`, `overlay/`, and optional `patches/`. The Sailfish OS version is an argument and applies to every recipe. `.env` is read from the working directory.

```text
go run ./cmd/ftuna 5.1.0.11 all
go run ./cmd/ftuna 5.1.0.11 github.com/sailfishos/sailfish-components-gallery sailfish-components-gallery
go run ./cmd/ftuna gc
go run ./cmd/ftuna scan <dir>
go run ./cmd/ftuna scratch register
go run ./cmd/ftuna scratch publish <dir> <name>
```

`all` publishes `repo/ftuna-oss/<version>/` and skips a recipe whose commit is already on S3. A single recipe takes a scratch name and publishes the RPMs it produced to `scratchpad-repo.ftuna.eu/<account>/<name>/`. Publishing that name again replaces the repo.

## Website

```text
https://ftuna.eu/                         # one page, with a link to the bucket root
https://repo-oss.ftuna.eu/                # repo/ftuna-oss/
https://repo-openrepos.ftuna.eu/          # mirror/openrepos/
https://scratchpad-repo.ftuna.eu/         # docs and directory listing at /, repos under /<account>/<id>/
https://cdn-mirror.ftuna.eu/              # cdn-mirror/
```