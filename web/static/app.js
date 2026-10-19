// gerve UI glue: theme toggle, ref switching, copy buttons. All features
// degrade without JavaScript.
(function () {
  "use strict";

  // Progressive-enhancement marker: CSS keys affordances that only work with
  // scripting off this class, so a scriptless page promises nothing it cannot
  // deliver.
  document.documentElement.classList.add("js");

  // Theme toggle: flip the light class, persist the choice. The inline
  // script in <head> already applied the initial state before first paint.
  var btn = document.getElementById("themeBtn");
  if (btn) {
    btn.addEventListener("click", function () {
      var light = document.documentElement.classList.toggle("light");
      try { localStorage.setItem("gerve-theme", light ? "light" : "dark"); } catch (e) {}
    });
  }

  // Ref switcher: jump to the same path on another ref.
  var sel = document.getElementById("refswitch");
  if (sel) {
    sel.addEventListener("change", function () {
      var base = sel.getAttribute("data-base") || "tree";
      var path = sel.getAttribute("data-path") || "";
      var ps = path
        ? "/" + path.split("/").map(encodeURIComponent).join("/")
        : "";
      location.href = "/" + base + "/" + encodeURIComponent(sel.value) + ps;
    });
  }

  // Copy buttons.
  document.querySelectorAll("[data-copy]").forEach(function (b) {
    b.addEventListener("click", function () {
      navigator.clipboard
        .writeText(b.getAttribute("data-copy"))
        .then(function () {
          b.classList.add("copied");
          setTimeout(function () { b.classList.remove("copied"); }, 900);
        })
        .catch(function () {});
    });
  });

  // Relative times ("5 minutes ago") carry their exact instant in datetime.
  // Render it in the viewer's own clock — only the browser knows the timezone —
  // and restate UTC in the same locale so the two popover lines read alike. The
  // server already put UTC in the markup, so the popover is useful either way.
  document.querySelectorAll("time.time[datetime]").forEach(function (el) {
    var when = new Date(el.getAttribute("datetime"));
    if (isNaN(when.getTime())) return;
    var opts = { dateStyle: "medium", timeStyle: "medium" };
    var local = el.querySelector(".time-local");
    var utc = el.querySelector(".time-utc");
    if (local) local.textContent = when.toLocaleString(undefined, opts);
    if (utc) {
      var utcOpts = { dateStyle: "medium", timeStyle: "medium", timeZone: "UTC" };
      utc.textContent = when.toLocaleString(undefined, utcOpts) + " UTC";
    }
  });

  // Addressable lines. Every code row carries id="L12", so the browser scrolls
  // to a line link by itself and :target tints it with no script at all. What
  // CSS cannot express is the rest: a range (#L10-L20), and re-applying the
  // highlight when the hash changes without a page load — which is what makes a
  // second permalink click, or the back button, move it rather than do nothing.
  var LINE_HASH = /^#L(\d+)(?:-L(\d+))?$/;

  function lineRange() {
    var m = LINE_HASH.exec(location.hash);
    if (!m) return null;
    var a = parseInt(m[1], 10);
    var b = m[2] ? parseInt(m[2], 10) : a;
    if (!(a > 0) || !(b > 0)) return null;
    return [Math.min(a, b), Math.max(a, b)];
  }

  function highlightLines() {
    document.querySelectorAll(".plaincode tr.hl").forEach(function (tr) {
      tr.classList.remove("hl");
    });
    var range = lineRange();
    if (!range) return;
    var first = null;
    var lines = 0;
    for (var n = range[0]; n <= range[1]; n++) {
      var tr = document.getElementById("L" + n);
      if (!tr) continue;
      tr.classList.add("hl");
      if (!first) first = tr;
      lines++;
    }
    // A range has no single element for the browser to scroll to, so the first
    // row it covers is brought into view; a lone line was already scrolled to
    // by :target targeting it, and re-scrolling would only make the page jump.
    if (first && lines > 1 && first.scrollIntoView) {
      first.scrollIntoView({ block: "center" });
    }
  }

  // Clicking a line number selects that line; shift-click extends the range from
  // the last line selected. The address is replaced rather than pushed, so a
  // spell of shift-clicking does not bury the back button while the address bar
  // always holds the link you would hand to someone else.
  var lastLine = null;

  function setHash(hash) {
    if (location.hash === hash) return;
    if (history.replaceState) history.replaceState(null, "", hash);
    else location.hash = hash;
  }

  document.addEventListener("click", function (e) {
    var cell = e.target.closest && e.target.closest(".plaincode td.ln");
    if (!cell) return;
    var tr = cell.parentNode;
    var n = parseInt((tr.id || "").slice(1), 10);
    if (!(n > 0)) return;
    var lo = n;
    var hi = n;
    if (e.shiftKey && lastLine) {
      lo = Math.min(lastLine, n);
      hi = Math.max(lastLine, n);
    }
    lastLine = n;
    setHash("#L" + lo + (hi > lo ? "-L" + hi : ""));
    highlightLines();
  });

  window.addEventListener("hashchange", highlightLines);
  highlightLines();
})();
