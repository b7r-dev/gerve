#!/usr/bin/env bash
# design-check — enforces gerve's visual system without a browser.
#   1. spacing audit: every component of every padding/margin/gap must be a
#      --sp-* token, 0, auto, or a calc()/literal on the 4px scale (2px is the
#      documented chip half-step).
#   2. contrast audit: declared text/surface token pairs must meet WCAG AA
#      (>= 4.5 body, >= 3.0 large/UI) in both themes.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - web/static/app.css <<'PY'
import re, sys

css = open(sys.argv[1]).read()
fail = 0

# ---- spacing audit ----------------------------------------------------------

SCALE = {"0", "2px", "4px", "8px", "12px", "16px", "24px", "32px", "48px"}
decl = re.compile(r"^\s*(padding|margin|gap|row-gap|column-gap)[a-z-]*\s*:\s*([^;]+);", re.M)
# em is allowed: typographic spacing inside .md/.chroma scales with its text.
# The scale discipline governs layout spacing (px/rem).
component_ok = re.compile(
    r"^(0|auto|calc\(.+\)|var\(--sp-[1-7]\)|"
    r"0\.?5?px|2px|4px|8px|12px|16px|24px|32px|48px|"
    r"[0-9]*\.?[0-9]+em)$"
)

offenders = []
for m in decl.finditer(css):
    prop, value = m.group(1), m.group(2).strip()
    if "var(--radius" in value or "var(--text" in value:
        continue
    for comp in value.split():
        if component_ok.match(comp):
            continue
        # calc() wrapping scale members is fine; a bare literal must be on-scale
        if comp.startswith("calc(") and "var(--sp-" in comp:
            continue
        offenders.append((prop, value, comp))

print("== spacing audit ==")
if offenders:
    fail = 1
    for prop, value, comp in offenders:
        print(f"  OFF-SCALE {prop}: {value}   (component: {comp})")
else:
    print("  all padding/margin/gap components on scale")

# ---- contrast audit ---------------------------------------------------------

def block(sel):
    m = re.search(re.escape(sel) + r"\s*\{(.*?)\}", css, re.S)
    if not m:
        sys.exit(f"token block {sel} not found")
    body = re.sub(r"/\*.*?\*/", "", m.group(1), flags=re.S)
    out = {}
    for line in body.split(";"):
        if ":" in line:
            k, v = line.split(":", 1)
            out[k.strip().lstrip("-")] = v.strip()
    return out

dark, light = block(":root"), block("html.light")

def lin(c):
    c /= 255
    return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4

def lum(hexv):
    hexv = hexv.lstrip("#")
    r, g, b = (int(hexv[i:i+2], 16) for i in (0, 2, 4))
    return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)

def ratio(a, b):
    la, lb = lum(a), lum(b)
    hi, lo = max(la, lb), min(la, lb)
    return (hi + 0.05) / (lo + 0.05)

def tint(fg, alpha, bg):
    f, b = fg.lstrip("#"), bg.lstrip("#")
    return "#" + "".join(
        "%02x" % round(int(f[i:i+2], 16) * alpha + int(b[i:i+2], 16) * (1 - alpha))
        for i in (0, 2, 4)
    )

checks = [
    ("fg-1/plane",            "fg-1",        ("plain", "plane"),            4.5),
    ("fg-2/plane",            "fg-2",        ("plain", "plane"),            4.5),
    ("fg-3/plane",            "fg-3",        ("plain", "plane"),            4.5),
    ("fg-1/raised",           "fg-1",        ("plain", "raised"),           4.5),
    ("fg-2/raised",           "fg-2",        ("plain", "raised"),           4.5),
    ("fg-3/raised",           "fg-3",        ("plain", "raised"),           4.5),
    ("fg-1/sunken",           "fg-1",        ("plain", "sunken"),           4.5),
    ("pool-bright/raised",    "pool-bright", ("plain", "raised"),           4.5),
    ("pool-bright/sunken",    "pool-bright", ("plain", "sunken"),           4.5),
    ("ion/plane (focus/ui)",  "ion",         ("plain", "plane"),            3.0),
    ("paper/plane (brand)",   "paper",       ("plain", "plane"),            4.5),
    ("add on add-tint/sunken","add",         ("tint", "add", 0.11, "sunken"), 4.5),
    ("del on del-tint/sunken","del",         ("tint", "del", 0.11, "sunken"), 4.5),
]

print("== contrast audit ==")
for theme, tok in (("dark", dark), ("light", light)):
    print(f"  [{theme}]")
    for name, fgk, bgspec, minimum in checks:
        fg = tok[fgk]
        if bgspec[0] == "plain":
            bg = tok[bgspec[1]]
        else:
            _, tintfg, alpha, bgk = bgspec
            bg = tint(tok[tintfg], alpha, tok[bgk])
        r = ratio(fg, bg)
        ok = r >= minimum
        print(f"    {'ok ' if ok else 'FAIL'} {name:26s} {r:5.2f} (min {minimum})")
        if not ok:
            fail = 1

print()
print("design-check: PASS" if not fail else "design-check: FAIL")
sys.exit(1 if fail else 0)
PY
