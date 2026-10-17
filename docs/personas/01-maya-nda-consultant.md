# 01 · Maya Okonkwo — the NDA consultant

> "The code never leaves this laptop. That's the whole point of me."

## Snapshot

| | |
|---|---|
| **Role** | Independent backend consultant, 8 years in |
| **Stack** | Go & Postgres; macOS; terminal + editor |
| **Repos** | Three active client projects, all contractually barred from any hosted forge |
| **Tool attitude** | Pays for good tools; drops any tool that phones home or needs a config file |

## Context

Maya's contracts say client source stays on her machine. No GitHub, no private
GitLab, not even a backup service she doesn't control. She's professionally
lonely: no PRs, no code review, no green squares. What she misses most is the
*browser view* — seeing her own work the way an outsider would: structure
first, then files, then history. Twice a month she also walks the client
through progress on a screen share, and a raw `git log` in a terminal doesn't
inspire confidence.

## Narrative — "the Friday demo"

**4:07 PM.** The auth middleware refactor is done: eleven commits, including a
rename of `internal/handlers/auth.go` to `internal/handlers/middleware.go`
that she is quietly proud of. She opens the client repo:

```
gerve -p 8080 . --open
```

A browser tab appears on the repo overview: README rendered, five recent
commits with author and relative date, branch summary. **Delight:** under a
second, and she typed nothing but the path.

**4:11 PM.** Zoom call with the client's CTO. She shares the window and opens
**Commits**. The log reads like a changelog — short hashes, relative dates,
one-line subjects. The CTO asks what the auth change actually touched. She
opens the refactor commit: the diff renders as *a rename plus a small patch*,
not nine hundred lines of red-and-green noise. **Delight:** rename detection
makes her look more organized than she felt on Wednesday.

**4:19 PM.** CTO: "Can we see everything under `internal/auth`?" She clicks
through the tree. Breadcrumbs make the drill-down legible at screen-share
resolution, and every file row shows *who last touched it and when* — she
never has to click in to answer "is this recent?"

**4:24 PM.** They open `internal/auth/mocks_gen.go` — nine thousand lines of
generated code. The page doesn't choke: gerve notices the size, renders a
capped preview, and shows a "file is large — open raw" banner. A hung tab
during a billable demo is a career event; this is the requirement.

**4:31 PM.** The CTO asks for "a link to this". She can't give one — gerve is
bound to her localhost, which is *exactly right* for an NDA client — and
screen-shares instead. **Gap:** occasionally she *does* want a colleague on
her LAN to see it. She wants that to be an explicit, deliberate act, never a
default.

**4:40 PM.** Call over. Ctrl-C. Nothing was written anywhere: no dotfile, no
cache, no history. The repo's `.git` is byte-for-byte untouched.

## What the narrative surfaces

### Functional
- Start on any repo with one command and one path; no init, no config.
- Overview page: rendered README + recent commits, presentable to a client.
- Commit log with author, date, subject; commit page with **rename-aware diff**.
- Tree pages with breadcrumbs and per-entry last-commit metadata.
- Large-file guard: capped preview + raw link instead of a locked tab.
- `--open` launches the browser; the URL is printed either way.

### Non-functional
- **localhost-only by default** — the NDA case is *why* it's the default.
- **Zero network egress, ever** — she lives behind client VPNs and proxies;
  nothing should "check for updates".
- **Zero disk writes** outside the process — no config, no cache dirs.
- Polished visual design; it will be on screen during billable conversations.
- Perceived-instant startup (< 1 s to a usable page).

### Edge cases
- Generated files (huge line counts) must not stall highlighting.
- Submodule directories render as tree entries; no recursion in v1.
- Running under a corporate proxy must change nothing (no egress to it).

### Future needs (out of v1)
- Opt-in LAN sharing (`-a`) with an explicit warning — for the "send a link"
  moment.
- Per-file history view ("show me this file's story") — the first feature
  she'll ask for after a month of use; feeds the v2 blame/file-history work.
