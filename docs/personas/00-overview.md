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
3. **Committed state is the truth (v1).** The UI renders git objects, not the
   worktree — this is what makes bare repos, old refs, and read-only mounts
   work for free. The filesystem's role in v1: repo discovery and future
   worktree features. Untracked/dirty overlay is a v2 question (OQ2).
4. **Degrade gracefully, never panic.** Every degenerate repo state (no
   commits, dangling HEAD, shallow clone, unknown encoding) gets a designed
   page, not a stack trace.
5. **localhost by default; widening is explicit.** 127.0.0.1 bind unless `-a`
   says otherwise.
6. **Performance budgets.** Cached tree < 150 ms, cold directory < 500 ms,
   log page < 300 ms, startup < 1 s. Bounded memory regardless of repo size.
7. **Rename-aware diffs are core.** Three of five personas independently need
   them; the git CLI is used precisely for this.

## Open questions for review

| # | Question | Recommendation |
|---|----------|----------------|
| OQ1 | Port already in use — fail or auto-increment? | **Fail with a hint** ("port 8080 busy — try -p 8081"); `-p 0` gives an OS-assigned port for scripted use. |
| OQ2 | Uncommitted/untracked files in the UI? | **v1 shows committed state only**; a worktree overlay is v2. Keeps bare/read-only support free. |
| OQ3 | Non-git directory — hard error or file-browser fallback? | **Exit 1 with a one-line hint.** No fallback browser in v1. |
| OQ4 | Refs with slashes (`ramon/webhooks`, `feature/old-ui`) in URLs | **Greedy longest-ref match** when parsing `/tree/{ref}/{path...}`; document the disambiguation. |
| OQ5 | Default bind | **127.0.0.1**, `-a` to widen, never silent. |
| OQ6 | Multi-repo index mode (serve a folder of repos) | **Defer to v2** — strongest roadmap signal (persona 04) but keeps v1 pure. |
| OQ7 | Auto-open browser | **`--open` opt-in**; always print the URL. |
| OQ8 | Directory-scoped commit log ("commits touching this dir") | **v2** — highest-demand future feature (personas 03, 05). |

## Explicitly deferred to v2+

Blame view, per-file history, directory-scoped log, multi-repo mode, LAN
sharing guide, uncommitted-worktree overlay, commit/file search, Atom feed,
snapshot export, Wails desktop wrapper.

## Assumptions to verify during implementation

- go-git resolves `.git`-as-a-file indirection (linked worktrees).
- The diff subprocess needs `-c safe.directory=...` handling when the repo
  owner differs from the running uid (persona 02).
- Multi-segment ref names can't use single-segment `ServeMux` wildcards —
  route `/tree/{rest...}` and split the remainder greedily against known refs.
- go-git has blame support but it's weak; v2 blame should use
  `git blame --porcelain`.
