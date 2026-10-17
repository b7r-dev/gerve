# gerve

A single-binary, read-only web UI for local git repositories.

`gerve -p 8080 .` starts a localhost HTTP server that renders the repository
you're standing in — file tree, blobs with syntax highlighting, commit log,
rename-aware diffs, blame, and per-file history — without the repo ever
leaving your machine.

Built for repositories that aren't (or can't be) pushed to a hosted forge:
private solo work, client code under NDA, air-gapped environments, giant
internal monorepos, old archives, and throwaway experiments.

## Status

**v1 working.** Requirements were explored through five personas and
narratives in [`docs/personas/`](docs/personas/) — start with
[the overview](docs/personas/00-overview.md) for the settled decisions.

## Installation

Needs Go 1.26 or newer.

From a checkout of this repository:

```sh
go install ./cmd/gerve
```

The binary lands in `$(go env GOPATH)/bin` — make sure that directory is on
your `PATH`:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

Or install from anywhere — no checkout needed — now that the module lives
on GitHub:

```sh
go install github.com/b7r-dev/gerve/cmd/gerve@latest
```

Or build a standalone binary by hand — a single static executable with every
template, stylesheet, and script embedded, no runtime dependencies:

```sh
CGO_ENABLED=0 go build -o /usr/local/bin/gerve ./cmd/gerve
```

Then point it at any repository:

```sh
gerve -p 8080 .
```

## Planned usage

```
gerve [flags] [path]

  -p <port>     listen port        (default 8080; 0 = OS-assigned)
  -a <addr>     bind address       (default 127.0.0.1; 0.0.0.0 = all
                                   interfaces — LAN URLs printed on start)
  -b <ref>      ref to open at     (default HEAD)
  --open        open the browser on start
  -V            print version
```

Flags work before or after the path: `gerve . -p 8080` is fine too.

## Design in one paragraph

go-git for object access (refs, trees, blobs); the `git` CLI for the things it
does best — rename-aware patches, blame, worktree status, and log with
`--follow`. Server-rendered `html/template` with all assets embedded via
`go:embed` — zero build step, zero network egress, one static binary.
localhost-only by default. Read-only: gerve never writes to the repository.

## What you'll see

- **Tree pages** with per-file last-commit info, README rendering at every
  level, and — when browsing HEAD — a live worktree overlay: untracked files
  appear, modified and deleted files are badged.
- **Blob pages** with syntax highlighting, line numbers, image previews, and
  links to raw, blame, and history.
- **Commit pages** with rename-aware patches, toggleable unified/side-by-side.
- **Commit log**, including directory-scoped (`?path=`) and `--follow` file
  history.
- **Blame** via `git blame --porcelain`.

## License

[MIT](LICENSE)
