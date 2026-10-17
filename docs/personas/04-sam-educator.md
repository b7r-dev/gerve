# 04 · Sam Whitfield — the educator

> "If it errors on a nineteen-year-old's repo, it's broken, not them."

## Snapshot

| | |
|---|---|
| **Role** | CS lecturer and bootcamp mentor; grades ~30 student repos a week |
| **Environment** | Whatever laptop is free; submissions arrive as zip files into `~/grading/wk4/` |
| **Repos** | Tiny but *hostile*: no commits yet, detached HEADs, images, CRLF, emoji filenames, one with no `.git` at all |
| **Tool attitude** | Reads no docs; `-h` or nothing |

## Context

Student repos are never pushed anywhere public — they're emailed, zipped, and
uploaded to the LMS. Sam's grading loop is: open repo, glance at structure,
read the README, check the commit history for work spread over time rather
than a Sunday-night dump. The tool must survive every mistake a beginner can
make, because beginners make all of them.

## Narrative — "grading night"

**7:00 PM.** Unzips `wk4`. For each repo:

```
cd jamie-assignment && gerve -p 8081 &
```

She wants thirty sequential (or parallel) runs to be trivial and independent.

**7:05 PM.** Repo A: the student committed nothing. gerve shows a *human*
empty state — "no commits yet — this repository has no history" — and whatever
it *can* show, instead of a 500.

**7:09 PM.** Repo B: detached HEAD from a hash checkout. The header honestly
labels it "detached at a1b2c3d"; everything still browsable.

**7:12 PM.** Repo C: no `.git` — the zip was made from a plain copy. The
terminal prints
`not a git repository: ~/grading/wk4/jamie-assignment (no .git found)` and
exits 1. No panic, no half-started server.

**7:15 PM.** Repo D: `assets/` contains PNGs. The blob page detects image
content and previews it inline — she sees the architecture diagram the student
drew without opening Preview.

**7:20 PM.** Repo E: commits dated three days *in the future* (clock skew),
authored by `student <unknown@localhost>`. Rendered faithfully — "in 3 days"
is fine, actually. Nothing crashes.

**7:40 PM.** Friction: a port collides with last week's forgotten gerve. She
never reads docs; the error has to teach:
`port 8080 busy — try: gerve -p 8081`. (Fail-with-hint vs auto-increment is
OQ1.)

**11:00 PM.** Thirty repos graded, zero stack traces. She never once opened a
README about gerve — because there didn't need to be one.

## What the narrative surfaces

### Functional
- Human empty/degenerate states: no commits, detached HEAD, no README, no
  refs — each gets designed copy, not an error.
- Non-repo paths: clean CLI error + exit code 1.
- Image blob previews (`image/*` rendered inline).
- `-h` is fully self-explanatory.
- Parallel/sequential instances without shared state or lock files.

### Non-functional
- **Never panics** — a recover middleware returns a 500 page naming the repo
  and path.
- Zero config; instant start/stop; no artifacts left behind.

### Edge cases
- Future-dated commits; unknown/placeholder authors.
- CRLF files; UTF-16; emoji and unicode filenames; extremely long lines
  (horizontal scroll, never a broken layout).
- Broken submodule entries.

### Future needs (out of v1)
- **Multi-repo index mode** — point gerve at `~/grading` and get a browsable
  list of repositories. The strongest roadmap signal of all five personas
  (OQ6).
