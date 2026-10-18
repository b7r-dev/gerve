// gerve UI glue: theme toggle, ref switching, copy buttons. All features
// degrade without JavaScript.
(function () {
  "use strict";

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
})();
