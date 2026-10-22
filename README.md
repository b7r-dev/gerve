# gerve

[![CI](https://github.com/b7r-dev/gerve/actions/workflows/ci.yml/badge.svg)](https://github.com/b7r-dev/gerve/actions/workflows/ci.yml)

**gerve is a local Git repository viewer:** a single-binary, read-only web UI for
the repository already on your machine.

`gerve -p 8080 .` starts a localhost HTTP server that renders the repository
you're standing in — file tree, blobs with syntax highlighting, commit log,
rename-aware diffs, blame, and per-file history — without the repo ever
leaving your machine.

Built for repositories that aren't (or can't be) pushed to a hosted forge:
private solo work, client code under NDA, air-gapped environments, giant
internal monorepos, old archives, and throwaway experiments.

## Why gerve?

`git instaweb` can browse a local repository, but brings gitweb and a web
server along for the ride. cgit and GitList are designed to be deployed.
Full forges such as Gitea solve a much larger problem.

gerve is for one repository already on your machine:

    cd some-repo
    gerve .

Nothing is imported, uploaded, indexed remotely, or written back to the repo.
gerve does not host repositories, import them, mutate them, or require an
account, a database, or a background service.

## Status

**v1.0.0 — stable.** The requirements that shaped it are recorded through five
personas and narratives in [`docs/personas/`](docs/personas/) — start with
[the overview](docs/personas/00-overview.md) for the settled decisions.

## Install

Download the archive for your platform from
[GitHub Releases](https://github.com/b7r-dev/gerve/releases), verify it against
`checksums.txt`, and put `gerve` on your `PATH`. Archives contain the binary,
`README.md`, and `LICENSE`.

Or install with the Go toolchain, with no checkout:

```sh
go install github.com/b7r-dev/gerve/cmd/gerve@latest
```

The binary lands in `$(go env GOPATH)/bin` — make sure that directory is on
your `PATH`:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

From a checkout, install into `GOPATH/bin`:

```sh
go install ./cmd/gerve
```

Or build a standalone binary by hand — a single static executable with every
template, stylesheet, and script embedded, no runtime dependencies:

```sh
CGO_ENABLED=0 go build -trimpath -o gerve ./cmd/gerve
```

## Requirements

- Go 1.26.1 or newer to build.
- `git` to read repositories.
- git 2.46 or newer for zero egress. gerve sets `GIT_NO_LAZY_FETCH` on every
  git call; older git ignores it, so a partial clone could fetch missing
  objects from its promisor remote.

## Usage

```
gerve [flags] [path]

  -p, --port <port>   listen port        (default 8080; 0 = OS-assigned)
  -a, --addr <addr>   bind address       (default 127.0.0.1; 0.0.0.0 = all
                                         interfaces — LAN URLs printed on start)
  -b, --ref <ref>     ref to open at     (default HEAD)
      --open          open the browser on start
  -V, --version       print version
  -h, --help          show this help
```

Flags work before or after the path: `gerve . -p 8080` is fine too.

```sh
gerve .
gerve -p 9000 ~/src/some-repo
gerve --version
```

## Development

Build, test, and validate with the standard Go toolchain:

```sh
go build ./cmd/gerve
go test ./...
go vet ./...
gofmt -l .
```

`gofmt -l .` should print nothing. CI runs tests and vet on Linux, macOS, and
Windows, and checks formatting on Linux.

### Snapshot release

Exercise the full GoReleaser configuration locally without publishing anything.
You need GoReleaser v2 installed (`brew install goreleaser`, or see the
[installation docs](https://goreleaser.com/install/)):

```sh
goreleaser release --snapshot --clean
```

Artifacts — archives for macOS and Linux on amd64 and arm64, Windows on amd64,
plus `checksums.txt` — land in `dist/`. Snapshot builds use the same
`.goreleaser.yaml` as a real release; only the version and publishing differ.

### Publishing a release

Release tags use semantic versioning and must match `v*`. Push the tag and let
GitHub Actions do the rest:

```sh
git tag v1.0.0
git push origin v1.0.0
```

The release workflow checks out the tag, sets up Go, runs GoReleaser, and
publishes a GitHub Release named after the tag. Binaries are built with
`CGO_ENABLED=0` and `-trimpath`, and the version is embedded at link time, so
`gerve --version` reports the release tag. Release notes are generated from the
commit history, grouped into features, bug fixes, and other changes; docs,
chore, test, style, design, CI, and merge commits are omitted. Prerelease tags
such as `v1.1.0-rc.1` are marked as prereleases automatically.

## Design in one paragraph

go-git for object access (refs, trees, blobs); the `git` CLI for the things it
does best — rename-aware patches, blame, worktree status, and log with
`--follow`. Server-rendered `html/template` with all assets embedded via
`go:embed` — zero build step, zero network egress, one static binary.
Stylesheet, script, and font URLs carry content hashes, so browsers cache
them hard and never serve a stale asset after an upgrade. localhost-only by
default. Read-only: gerve never writes to the repository.

## What you'll see

- **Tree pages** with per-file last-commit info, README rendering at every
  level, and — when browsing HEAD — a live worktree overlay: untracked files
  appear, modified and deleted files are badged. A directory that exists only
  in the working tree is listed from disk, with gitignored entries counted but
  never shown or served.
- **Blob pages** with syntax highlighting, line numbers, image previews, and
  links to raw, blame, and history.
- **Commit pages** with rename-aware patches, toggleable unified/side-by-side.
- **Compare** any two refs — `/compare/main...feature` — with the diff taken from
  their merge base, the same thing a pull request calls "files changed". The
  commit list, per-file cards, and a jump-to-file index come with it.
- **Commit log**, including directory-scoped (`?path=`) and `--follow` file
  history.
- **Search** for a literal string, scoped to a directory or the whole
  repository. Results are line links: each match opens the file view at that
  line, already highlighted. Tracked files are searched as committed — the same
  bytes the file view shows — and files that exist only in your working tree are
  searched too, badged as such.
- **Blame** via `git blame --porcelain`.

Type set in [IBM Plex Sans](https://github.com/IBM/plex) and
[IBM Plex Mono](https://github.com/IBM/plex), vendored under
`web/static/fonts/` and embedded in the binary —
[SIL Open Font License 1.1](web/static/fonts/OFL.txt).

## License

[MIT](LICENSE)
