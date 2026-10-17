# Personas & narratives — how to read this folder

Before writing gerve code, we invented five users whose situations make a
local, read-only repo browser *necessary* rather than merely nice. Each persona
has a narrative — a concrete story of using gerve — and each narrative doubles
as a requirements probe: features are validated by watching them get used, and
gaps show up as friction instead of speculation.

Read 01–05 in order; each stresses a different corner. This file collects what
they jointly demand and lists the open questions to settle before
implementation.

## The five

| # | Persona | Situation | What their narrative stress-tests |
|---|---------|-----------|-----------------------------------|
| 01 | [Maya Okonkwo](01-maya-nda-consultant.md) | Freelancer; client code under NDA never leaves her laptop | Polish, privacy defaults, demo-readiness, rename-aware diffs |
| 02 | [Dmitri Volkov](02-dmitri-air-gapped.md) | Engineer on an air-gapped dev VM, no outbound internet | Offline purity, single binary, verifiable no-egress, read-only mounts |
| 03 | [Priya Raghavan](03-priya-monorepo-onboarder.md) | New hire reading a 12 GB internal monorepo | Scale, lazy tree listing, caching, path-preserving ref switches, large files |
| 04 | [Sam Whitfield](04-sam-educator.md) | Lecturer grading 30 student repos a week | Graceful degradation, empty/broken repos, trivial CLI, image previews |
| 05 | [Elena Marchetti](05-elena-archivist.md) | Archivist of 15 years of her own projects, many bare clones | Bare repos, dangling HEAD, tags, old encodings, deep history |

## Requirement themes by persona

| Theme | 01 | 02 | 03 | 04 | 05 |
|---|:-:|:-:|:-:|:-:|:-:|
| Instant start, zero config | ● | ● | ● | ● | ● |
| localhost-only default, explicit widening | ● | ● | | | |
| Zero network egress, all assets embedded | ● | ● | | | |
| Single static binary (CGO off) | | ● | | ● | |
| Rename-aware diffs are core, not polish | ● | | | | ● |
| Graceful degradation, never panic | | ● | | ● | ● |
| Scale: lazy listing, caching, budgets | | ● | ● | | |
| Bare repos & odd refs | | | | | ● |
| Large-file guard | ● | | ● | ● | |
| Human empty states | | | | ● | ● |

## Cross-cutting requirements (all five agree)

1. **Read-only, verifiably.** Nothing is ever written to the repository,
   `$HOME`, or anywhere else. go-git reads objects; the only subprocess is a
   read-only `git diff/show` invocation.
2. **Instant start, zero config.** One command, one optional path. No init, no
   dotfiles, no first-run wizard.
3. **Git objects + live worktree, merged.** Content is served from git
   objects; when browsing HEAD, the worktree is overlaid — untracked files
   appear, modified and deleted files are badged. Bare repos and read-only
   snapshots automatically degrade to committed-only. Status comes from the
   CLI (`--porcelain -z`), is cached, and refreshes on a short TTL.
4. **Degrade gracefully, never panic.** Every degenerate repo state (no
   commits, dangling HEAD, shallow clone, unknown encoding) gets a designed
   page, not a stack trace.
5. **localhost by default; widening is explicit.** 127.0.0.1 bind unless `-a`
   says otherwise.
6. **Performance budgets.** Cached tree < 150 ms, cold directory < 500 ms,
   log page < 300 ms, startup < 1 s. Bounded memory regardless of repo size.
7. **Rename-aware diffs are core.** Three of five personas independently need
   them; the git CLI is used precisely for this.

## Decisions — settled in review

| # | Question | Decision |
|---|----------|----------|
| OQ1 | Port already in use — fail or auto-increment? | **Fail with a hint** ("port busy — try -p N+1"); `-p 0` gives an OS-assigned port for scripted use. |
| OQ2 | Uncommitted/untracked files in the UI? | **Full worktree overlay in v1** (chosen over committed-only and the middle dirty-badge option). Untracked files render from the filesystem; bare/read-only repos degrade to committed-only automatically. |
| OQ3 | Non-git directory | **Hard error**, exit 1 with a one-line hint. |
| OQ4 | Refs with slashes in URLs | **Greedy longest-ref match** against known refs when parsing `/tree/{ref}/{path...}`. |
| OQ5 | Default bind | **127.0.0.1**, `-a` to widen, never silent. |
| OQ6 | Multi-repo index mode | **v2.** |
| OQ7 | Auto-open browser | **`--open` opt-in**; always print the URL. |
| OQ8 | Directory-scoped commit log | **v1** (upgraded from the v2 recommendation during review). |

Additional decisions from review:

- **Primary persona: Maya** (private solo repos) — privacy and polish win
  trade-offs.
- **Blame view: v1** (via `git blame --porcelain`).
- **Per-file history: v1** (`git log --follow`).
- **Theme: system preference with a manual toggle** (localStorage-persisted).
- **Diff layout: unified and side-by-side, toggleable** (server-side `?view=`
  param, works without JavaScript).
- **License: MIT.**

## Explicitly deferred to v2+

Multi-repo mode, LAN sharing guide, commit/file search, Atom feed, snapshot
export, uncommitted-change indicators on non-HEAD refs, Wails desktop wrapper.

## Assumptions to verify during implementation

- go-git resolves `.git`-as-a-file indirection (linked worktrees).
- The diff subprocess needs `-c safe.directory=...` handling when the repo
  owner differs from the running uid (persona 02).
- Multi-segment ref names can't use single-segment `ServeMux` wildcards —
  route `/tree/{rest...}` and split the remainder greedily against known refs.
- go-git has blame support but it's weak; v2 blame should use
  `git blame --porcelain`.
