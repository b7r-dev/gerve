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

  // Ref switcher: jump to the same path on another ref. The search results page
  // is scoped by a query and a path that live in the query string rather than
  // the URL path, so they ride along — otherwise switching refs there would
  // silently drop the search and land on an empty form.
  var sel = document.getElementById("refswitch");
  if (sel) {
    sel.addEventListener("change", function () {
      var base = sel.getAttribute("data-base") || "tree";
      var path = sel.getAttribute("data-path") || "";
      var ps = path
        ? "/" + path.split("/").map(encodeURIComponent).join("/")
        : "";
      var extra = "";
      var q = sel.getAttribute("data-query") || "";
      if (q) {
        extra = "?q=" + encodeURIComponent(q);
        var scope = sel.getAttribute("data-scope") || "";
        if (scope) extra += "&path=" + encodeURIComponent(scope);
      }
      location.href = "/" + base + "/" + encodeURIComponent(sel.value) + ps + extra;
    });
  }

  // "/" focuses the search box — the one shortcut every tool of this kind has —
  // or opens the search page where the box is not rendered, which is every width
  // below the one that has room for it.
  document.addEventListener("keydown", function (e) {
    if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) return;
    var t = e.target;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
    var box = document.querySelector(".searchbox input[name=q]");
    if (box && box.getBoundingClientRect().width > 0) {
      e.preventDefault();
      box.focus();
      return;
    }
    var link = document.querySelector(".searchlink");
    if (link) {
      e.preventDefault();
      location.href = link.getAttribute("href");
    }
  });

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
  // CSS cannot express is the rest: ranges, re-applying the highlight when the
  // hash changes without a page load (which is what makes a second permalink
  // click, or the back button, move it rather than do nothing), and clicking a
  // line number to select it.
  //
  // The selection class is "line-sel", deliberately not "hl": that name belongs
  // to chroma's token namespace inside .chroma, where it means a highlighted
  // source line and brings display: block. A row wearing it stops being a table
  // row and its cells leave their columns.
  var RANGE = /^L(\d+)(?:-L(\d+))?$/;

  // Ranges may be comma-separated (#L1-L3,L9): a link that collects line
  // references acquires them, so parsing yields a list rather than one span.
  function lineRanges() {
    var body = location.hash.replace(/^#/, "");
    if (!body) return null;
    var ranges = [];
    body.split(",").forEach(function (part) {
      var m = RANGE.exec(part.trim());
      if (!m) return;
      var a = parseInt(m[1], 10);
      var b = m[2] ? parseInt(m[2], 10) : a;
      if (!(a > 0) || !(b > 0)) return;
      ranges.push([Math.min(a, b), Math.max(a, b)]);
    });
    return ranges.length ? ranges : null;
  }

  function inRanges(ranges, n) {
    for (var i = 0; i < ranges.length; i++) {
      if (n >= ranges[i][0] && n <= ranges[i][1]) return true;
    }
    return false;
  }

  function highlightLines() {
    document.querySelectorAll(".plaincode tr.line-sel").forEach(function (tr) {
      tr.classList.remove("line-sel");
    });
    var ranges = lineRanges();
    if (!ranges) return;
    // Walk the rows rather than the numbers: a hand-edited link can name any
    // number at all (#L1-L2000000000), and stepping through that would freeze
    // the tab. The rows bound the work instead.
    var rows = document.querySelectorAll(".plaincode tr");
    var first = null;
    for (var i = 0; i < rows.length; i++) {
      var n = parseInt((rows[i].id || "").slice(1), 10);
      if (!(n > 0) || !inRanges(ranges, n)) continue;
      rows[i].classList.add("line-sel");
      if (!first) first = rows[i];
    }
    // A lone line is an element the browser has already scrolled to, honouring
    // scroll-padding-top; a range is not an element at all, so bring its first
    // row into view — centred, which is what clears the sticky header there.
    var spansLines = ranges.some(function (r) { return r[1] > r[0]; });
    if (first && spansLines && first.scrollIntoView) {
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

  // Share dialog: phone-readable QR codes for the page you are looking at.
  // The server decides whether sharing exists at all; this only renders the
  // already-authorized origins and keeps an optional public-URL override in
  // this browser's localStorage. Nothing is sent back to the server.
  var shareBtn = document.getElementById("shareBtn");
  var shareDialog = document.getElementById("shareDialog");
  if (shareBtn && shareDialog && typeof qrcode === "function") {
    var shareList = document.getElementById("shareList");
    var shareEmpty = document.getElementById("shareEmpty");
    var shareInput = document.getElementById("sharePublicURL");
    var shareOrigins = document.getElementById("shareOrigins");
    var shareQR = document.getElementById("shareQR");
    var shareQRCode = document.getElementById("shareQRCode");
    var shareQRLabel = document.getElementById("shareQRLabel");
    var shareKey = "gerve-share-url";
    var defaultPublic = shareDialog.getAttribute("data-public-url") || "";

    function storedShareURL() {
      try {
        return localStorage.getItem(shareKey);
      } catch (e) {
        return null;
      }
    }

    function normalizeShareOrigin(raw) {
      raw = (raw || "").trim();
      if (!raw) return "";
      var hadScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(raw);
      if (!hadScheme) raw = "http://" + raw;
      var u;
      try {
        u = new URL(raw);
      } catch (e) {
        return "";
      }
      if (u.protocol !== "http:" && u.protocol !== "https:") return "";
      if (u.username || u.password) return "";
      if (u.pathname !== "" && u.pathname !== "/") return "";
      if (u.search || u.hash) return "";
      if (!hadScheme && !u.port && u.protocol === "http:" && location.port) {
        u.port = location.port;
      }
      return u.origin;
    }

    function savedOrigins() {
      var out = [];
      shareOrigins.querySelectorAll("[data-origin]").forEach(function (el) {
        var origin = el.getAttribute("data-origin");
        if (origin && origin !== defaultPublic && out.indexOf(origin) < 0) out.push(origin);
      });
      return out;
    }

    function effectiveOrigins() {
      var out = [];
      var custom = normalizeShareOrigin(shareInput.value);
      if (custom) out.push(custom);
      savedOrigins().forEach(function (origin) {
        if (out.indexOf(origin) < 0) out.push(origin);
      });
      return out;
    }

    function pageURL(origin) {
      return origin + location.pathname + location.search + location.hash;
    }

    function showQR(url) {
      shareQR.hidden = false;
      shareQRLabel.textContent = url;
      shareQRCode.textContent = "";
      try {
        var qr = qrcode(0, "M");
        qr.addData(url);
        qr.make();
        shareQRCode.innerHTML = qr.createSvgTag({
          cellSize: 1,
          margin: 4,
          scalable: true,
          alt: url,
        });
      } catch (e) {
        shareQRCode.textContent = "Could not generate a QR code for this URL.";
      }
    }

    function copyURL(url, button) {
      if (!navigator.clipboard) return;
      navigator.clipboard.writeText(url).then(function () {
        var old = button.textContent;
        button.textContent = "copied";
        setTimeout(function () { button.textContent = old; }, 900);
      }).catch(function () {});
    }

    function renderShare() {
      var origins = effectiveOrigins();
      shareQR.hidden = true;
      shareList.textContent = "";
      shareEmpty.hidden = origins.length > 0;
      origins.forEach(function (origin) {
        var url = pageURL(origin);
        var row = document.createElement("li");
        row.className = "share-origin";

        var code = document.createElement("code");
        code.className = "share-url";
        code.textContent = url;

        var copy = document.createElement("button");
        copy.type = "button";
        copy.className = "share-action";
        copy.textContent = "copy";
        copy.addEventListener("click", function () { copyURL(url, copy); });

        var qr = document.createElement("button");
        qr.type = "button";
        qr.className = "share-action";
        qr.textContent = "QR";
        qr.setAttribute("aria-label", "show QR code for " + url);
        qr.addEventListener("click", function () { showQR(url); });

        row.appendChild(code);
        row.appendChild(copy);
        row.appendChild(qr);
        shareList.appendChild(row);
      });
    }

    var saved = storedShareURL();
    if (saved !== null) {
      if (saved === "" || normalizeShareOrigin(saved)) {
        shareInput.value = saved;
      } else {
        try { localStorage.removeItem(shareKey); } catch (e) {}
      }
    }

    shareInput.addEventListener("change", function () {
      var value = shareInput.value.trim();
      if (!value) {
        shareInput.setCustomValidity("");
        shareInput.value = "";
        try { localStorage.setItem(shareKey, ""); } catch (e) {}
        renderShare();
        return;
      }
      var normalized = normalizeShareOrigin(value);
      if (!normalized) {
        shareInput.setCustomValidity("Enter an http or https origin, for example http://host:8080");
        shareInput.reportValidity();
        return;
      }
      shareInput.setCustomValidity("");
      shareInput.value = normalized;
      try { localStorage.setItem(shareKey, normalized); } catch (e) {}
      renderShare();
    });
    shareInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault();
        shareInput.blur();
      }
    });

    shareBtn.hidden = false;
    shareBtn.addEventListener("click", function () {
      shareInput.setCustomValidity("");
      renderShare();
      if (typeof shareDialog.showModal === "function") shareDialog.showModal();
      else shareDialog.setAttribute("open", "");
    });
    function closeShare() {
      if (typeof shareDialog.close === "function") shareDialog.close();
      else shareDialog.removeAttribute("open");
    }
    document.getElementById("shareClose").addEventListener("click", closeShare);
    shareDialog.addEventListener("click", function (e) {
      if (e.target === shareDialog) closeShare();
    });
  }
})();
