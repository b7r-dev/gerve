# 05 · Elena Marchetti — the archivist

> "Fifteen years of work is sitting in ~/src/archive and nowhere else."

## Snapshot

| | |
|---|---|
| **Role** | Designer-turned-engineer; hobbyist historian of her own output |
| **Environment** | Mac mini; `~/src/archive` full of project dumps, bare clones, SVN-era conversions, repos from three dead hosting services |
| **Repos** | Ancient, odd, and beloved: bare clones, dangling refs, latin-1 sources, tags like `v0.9-2014` |
| **Tool attitude** | Patient and exploratory; allergic to tools that assume a hot repo on a fresh `main` |

## Context

Her old repos were never hosted: laptops died, forges shut down, and some
work was client-confidential anyway. She revisits them to recover ideas — and,
when ex-coworkers' repos land in her archive, to answer "who wrote this?".
The tool that serves her must treat a 2011 repo as a first-class citizen.

## Narrative — "the rain Sunday"

**2:00 PM.** She finds `dotgrid.git` — a bare clone from 2014.

```
gerve ~/src/archive/dotgrid.git
```

Bare-repo support means tree, log, and blob all work from git objects alone;
there is no worktree and none is needed.

**2:05 PM.** `HEAD` is symbolic to a branch that was deleted years ago.
Instead of an error, gerve shows a recovery page: "HEAD points at a missing
branch — pick one", with every ref listed by last-commit recency.
**Delight:** the tool meets the repo where it is.

**2:10 PM.** She opens tag `v0.9-2014`: an annotated tag, so she gets the tag
message and the commit it names.

**2:15 PM.** The era's readme is `README.txt` — rendered as preformatted text,
not mangled by a markdown parser.

**2:25 PM.** She follows `core/grid.go` across two renames in commit diffs —
rename detection again. Three of five personas needed it independently; it's
core, not polish.

**2:40 PM.** An old commit by a colleague who left the industry. She wants
blame — not in v1. She reads the commit page instead. **Gap:** v2 blame.

**3:00 PM.** One archive has `.git` as a *file* — a linked worktree pointing
elsewhere. gerve resolves the indirection (or explains it clearly if it
can't).

**3:15 PM.** A latin-1 encoded 2003-era source file renders safely — escaped
bytes with a charset note, not mojibake-as-HTML and not a crash.

## What the narrative surfaces

### Functional
- Bare repository support (objects-only browsing).
- Dangling-HEAD recovery UI: list refs, pick one, never dead-end.
- Tag browsing including annotated tag messages.
- Non-markdown READMEs rendered as text.
- Rename-aware diffs; deep pagination back through years of log.

### Non-functional
- Robustness over gloss: fifteen-year-old repos are first-class.
- Unknown encodings never crash and never inject HTML.
- A long-running server left open all afternoon is normal.

### Edge cases
- `.git`-as-a-file indirection (linked worktrees).
- Refs with slashes (`feature/old-ui`) in URLs — OQ4 again.
- Empty trees at old tags; dead-hosting-era `.git` layouts.
- latin-1 / UTF-16 / unknown encodings.

### Future needs (out of v1)
- **Blame view** — archaeology is her entire use case.
- Commit search by author and date range.
- Snapshot export (zip a ref's tree).
