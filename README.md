# gerve

A single-binary, read-only web UI for local git repositories.

`gerve -p 8080 .` starts a localhost HTTP server that renders the repository
you're standing in — file tree, blobs with syntax highlighting, commit log,
and rename-aware diffs — without the repo ever leaving your machine.

Built for repositories that aren't (or can't be) pushed to a hosted forge:
client code under NDA, air-gapped environments, giant internal monorepos, old
archives, and throwaway experiments.

## Status

**Requirements phase.** Implementation starts after the personas and
narratives in [`docs/personas/`](docs/personas/) are reviewed. Start with
[the overview](docs/personas/00-overview.md).

## Planned usage

```
gerve [flags] [path]

  -p <port>     listen port        (default 8080)
  -a <addr>     bind address       (default 127.0.0.1)
  -b <ref>      ref to open at     (default HEAD)
  --open        open the browser on start
  -V            print version
```

## Design in one paragraph

go-git for object access (refs, trees, log, blobs); the `git` CLI only for
rename-aware patch generation. Server-rendered `html/template` with all assets
embedded via `go:embed` — zero build step, zero network egress, one static
binary. localhost-only by default. Read-only: gerve never writes to the
repository.
