# 03 · Priya Raghavan — the monorepo onboarder

> "Week one is reading. The internal UI takes eleven seconds to load a tree — I'll read locally."

## Snapshot

| | |
|---|---|
| **Role** | Senior engineer, week one at a company with a 12 GB monorepo |
| **Environment** | M-series MacBook; monorepo cloned to `~/code/mono`; hosted-forge access still pending permissions |
| **Repos** | One enormous internal monorepo; 40k+ tracked files, dozens of services |
| **Tool attitude** | Keyboard-driven, speed-hungry, adopts anything that respects her time |

## Context

Her company's code isn't on any public forge and the internal web UI is slow
and VPN-bound. She has a fresh local clone and a goal: build a mental map of
the system — structure, activity, and READMEs at every level — before she
writes a line.

## Narrative — "mapping the territory"

**9:00 AM.** `gerve -p 8080 ~/code/mono`. The top-level tree appears in under
200 ms despite 40k files — listing is per-directory and cached, not a full
walk. **Delight:** the internal UI's eleven-second tree load is now a bad
memory.

**9:05 AM.** Root README renders. She drills services → payments and finds
`services/payments/README.md` rendered *on the tree page* — nested READMEs
are how a monorepo explains itself.

**9:20 AM.** She clicks a 40 MB JSON fixture. gerve shows metadata and a raw
download instead of freezing the tab.

**9:40 AM.** A colleague's branch has the new webhook service. She switches
refs from the header switcher while deep in
`services/payments/handlers/webhook.go` — and lands on the **same path** on
the new branch. Path-preserving ref switching is the moment she decides to
keep gerve open all week.

**9:45 AM.** Before switching she opens **compare** for `main...ramon/webhooks`,
scoped to `services/payments`. The diff starts from the merge base, so it is the
branch's own work rather than main's; the commit list and a jump-to-file index
sit above the cards. **Delight:** she reviews the change without disturbing her
own checkout, which is exactly what the forge would have asked of her.

**10:15 AM.** She wants "recent commits touching this directory". Not in v1 —
she copies the path, goes to Commits, and browser-finds. **Gap:** the
highest-value missing feature; goes on the roadmap.

**11:00 AM.** The tree shows `shared -> ../vendor/shared`. Clicking it shows
the link target (git stores symlinks as blobs) and does not follow it out of
the repo — sane and safe.

**12:30 PM.** She's also *coding* in this repo. She commits at noon; her 2 PM
tree view shows the new commit. Cache invalidation works fast enough that the
tool feels live.

**4:00 PM.** Friction: two teammates' branches are named with slashes
(`ramon/webhooks`). URLs and the ref switcher must survive multi-segment ref
names.

## What the narrative surfaces

### Functional
- Lazy per-directory tree listing with caching (never a full-repo walk).
- Nested READMEs rendered on tree pages.
- **Path-preserving ref switching** — switch branches, keep your place.
- Compare two refs from their merge base, path-scoped, with the commit list and a
  jump-to-file index.
- Large-file handling: metadata + raw download, never a frozen tab.
- Symlink entries: show the target, don't follow it.
- Breadcrumbs; clean URLs.

### Non-functional
- Latency budgets: cached tree < 150 ms, cold directory < 500 ms, log page
  < 300 ms.
- Bounded memory on a 12 GB repo; caching with freshness within seconds of
  new commits (she's committing while browsing).

### Edge cases
- Ref names containing slashes in URLs (greedy ref resolution — see OQ4).
- Unicode and spaces in paths (correct URL encoding everywhere).
- Case-only collisions (`README.md` vs `readme.md`) on a case-insensitive Mac.
- Untracked files absent from the git tree: committed state is truth in v1
  (OQ2) — the narrative explicitly assumes this.

### Future needs (out of v1)
- **Directory-scoped commit log** ("commits touching this dir") — her top ask.
- Untracked-file overlay toggle; deep-link search.
