# 02 · Dmitri Volkov — the air-gapped engineer

> "If it opens a socket I didn't ask for, it doesn't stay installed."

## Snapshot

| | |
|---|---|
| **Role** | Staff engineer at a payments company, regulated environment |
| **Environment** | Hardened Linux dev VM; no outbound internet; tools arrive via an internal artifact mirror |
| **Repos** | Internal only; some 15k+ files; an occasional read-only NFS snapshot of an archive |
| **Tool attitude** | Reads a binary's strings before trusting it; single static binaries strongly preferred |

## Context

His org forbids outbound connections from dev VMs. An internal GitLab exists
but is slow, permission-gated, and half his work involves repos staged locally
during migrations anyway. He wants a repo browser that is *provably silent*:
a static binary, embedded assets, one listening socket he chose, connections
nowhere.

## Narrative — "the audit"

**Monday.** He pulls `gerve` from the internal mirror: a single ~8 MB static
binary, CGO disabled, no runtime dependencies. `gerve -V` prints a version —
the audit trail requires it. Installs to `~/bin`. Done.

**9:20 AM.** Runs it against the ledger-service repo (18k files):

```
gerve -p 7781 .
```

Then he verifies silence: the org's egress monitor shows nothing, and
`ss -tnp` shows exactly one socket — LISTEN on 127.0.0.1:7781, zero outbound.
**Delight:** trust established in minute one. He later greps the binary for
`https://` and finds no CDN URLs — every asset is embedded.

**9:35 AM.** Browses. Tree pages are fast at every depth because listing is
per-directory and cached, never a full 18k-file walk. The log paginates
instead of rendering five thousand commits.

**10:10 AM.** He mounts a read-only NFS snapshot of an archived repo and
points gerve at it. It opens fine and reads full history — gerve never tried
to write to `.git`. (He notes that `git` itself complains about "dubious
ownership" here; gerve's go-git core doesn't care, but he'd want the diff
subprocess handled just as gracefully — see edge cases.)

**11:00 AM.** From his laptop he `ssh -L`-forwards into the VM and browses
there. The localhost default is exactly what makes the tunnel trivial.

**2:00 PM.** For a containerized service repo he runs gerve *inside* the
container and needs `-a 0.0.0.0` to reach it from the host. He wants that to
be explicit and slightly loud — never a silent default.

**3:30 PM.** A CI-produced shallow clone: gerve shows the commits that exist
and says "shallow clone — history truncated" instead of pretending.

## What the narrative surfaces

### Functional
- `-V` prints a version (mirror + audit requirement).
- `-a` for explicit bind widening (container case); localhost default intact.
- Pagination everywhere; bounded work per page.
- Shallow-clone notice; read-only-mount tolerance.

### Non-functional
- **Fully offline**: no CDN fonts, scripts, images, or favicon; everything
  embedded in the binary.
- **Verifiable no-egress**: one listening socket, zero outbound connections,
  ever.
- Single static binary, `CGO_ENABLED=0`, no runtime deps.
- Container-friendly: no `$HOME` writes, honors env, runs as any uid.
- Bounded memory regardless of repo size.

### Edge cases
- Read-only `.git`; packed-refs-only repos; running as a different uid than
  the repo owner (the diff subprocess must handle `safe.directory` rejections
  gracefully rather than serve broken diffs).
- Shallow clones (history genuinely absent — say so).

### Future needs (out of v1)
- Dirty-worktree badge — he wants to see what hasn't been committed yet,
  even if v1's read-only-committed-state stance is correct for browsing.
