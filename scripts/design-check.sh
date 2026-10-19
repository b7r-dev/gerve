#!/usr/bin/env bash
# design-check — enforces gerve's visual system without a browser.
#   1. spacing audit: every component of every padding/margin/gap must be a
#      --sp-* token, 0, auto, or a calc()/literal on the 4px scale (2px is the
#      documented chip half-step).
#   2. type audit: every font-size (declared, or the size slot of a font
#      shorthand) must be a --text-* token. Sizes are chosen from the scale,
#      never derived from the surrounding text with em/%/calc.
#   3. weight audit: every declared font-weight must name a weight an embedded
#      face actually provides, so a rule never resolves to a face it did not
#      ask for (500 in mono renders as 400, 700 in mono as 600).
#   4. contrast audit: declared text/surface token pairs must meet WCAG AA
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

# ---- type audit -------------------------------------------------------------

# Sizes come from the scale: a step is picked by name, never computed from the
# text around it (0.85em, calc(var(--text-sm) * 1.1), …). The one indirection
# allowed is --text-code, which names the code step for what it is.
bare = re.sub(r"/\*.*?\*/", "", css, flags=re.S)
token = re.compile(r"^var\(--text-[a-z0-9]+\)$")
declared = re.compile(r"font-size\s*:\s*([^;]+);")
shorthand = re.compile(r"(?<![-a-z])font\s*:\s*([^;]+);")

type_offenders = []
for m in declared.finditer(bare):
    value = m.group(1).strip()
    if not token.match(value):
        type_offenders.append(("font-size", value))
for m in shorthand.finditer(bare):
    parts = m.group(1).split()
    # in `font: <weight> <size>/<line-height> <family>`, the size is the
    # component carrying the slash, or the component naming a text token
    size = next((p for p in parts if "/" in p), None)
    size = size.split("/")[0] if size else next((p for p in parts if p.startswith("var(--text-")), None)
    if size is None or not token.match(size):
        type_offenders.append(("font", m.group(1).strip()))

print("== type audit ==")
if type_offenders:
    fail = 1
    for prop, value in type_offenders:
        print(f"  OFF-SCALE {prop}: {value}   (must be var(--text-*))")
else:
    counts = {s: 0 for s in ("2xs", "xs", "sm", "base", "md", "lg", "xl")}
    for m in list(declared.finditer(bare)) + list(shorthand.finditer(bare)):
        for t in re.findall(r"var\(--text-([a-z0-9]+)\)", m.group(1)):
            key = "2xs" if t == "code" else t  # --text-code names the 2xs step
            counts[key] = counts.get(key, 0) + 1
    print("  every font-size is a --text-* token")
    print("  steps in use: " + " ".join(f"{k}={v}" for k, v in counts.items()))
    missing = [k for k, v in counts.items() if v == 0]
    if missing:
        fail = 1
        print(f"  UNUSED step(s): {', '.join(missing)} — a step with no job is dead weight")

# ---- weight audit -----------------------------------------------------------

# A weight no embedded face provides is answered with a different face than the
# rule names: 500 in mono renders as 400 and 700 in mono renders as 600, so the
# declaration lies about what the page looks like. The shipped set comes from
# the @font-face blocks themselves, so adding a face widens what is allowed.
faces = set()
for m in re.finditer(r"@font-face\s*\{(.*?)\}", bare, re.S):
    faces.update(re.findall(r"font-weight\s*:\s*(\d+)", m.group(1)))
body = re.sub(r"@font-face\s*\{.*?\}", "", bare, flags=re.S)

weight_offenders = []
for m in re.finditer(r"font-weight\s*:\s*([^;]+);", body):
    w = m.group(1).strip()
    if w not in faces and w not in ("normal", "inherit"):
        weight_offenders.append(("font-weight", w))
for m in shorthand.finditer(body):
    # a weight is only present in a font shorthand as a leading number/keyword
    head = m.group(1).split("/")[0].split()
    w = next((p for p in head if p.isdigit() or p in ("bold", "bolder", "lighter", "normal")), None)
    if w and w.isdigit() and w not in faces:
        weight_offenders.append(("font", m.group(1).strip()))

print("== weight audit ==")
print(f"  faces shipped: {', '.join(sorted(faces, key=int))}")
if weight_offenders:
    fail = 1
    for prop, value in weight_offenders:
        print(f"  UNSHIPPED {prop}: {value}   (no face at that weight)")
else:
    print("  every declared weight names a shipped face")

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
