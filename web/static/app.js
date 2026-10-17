// gerve UI glue: theme toggle, ref switching, copy buttons. All features
// degrade without JavaScript.
(function () {
  "use strict";

  // Theme toggle: cycle dark/light, persist the choice. The initial class
  // was already set inline in <head> before first paint.
  var btn = document.getElementById("themeBtn");
  if (btn) {
    btn.addEventListener("click", function () {
      var root = document.documentElement;
      var cur = root.classList.contains("light") ? "light" : "dark";
      var next = cur === "light" ? "dark" : "light";
      root.classList.remove(cur);
      root.classList.add(next);
      try { localStorage.setItem("gerve-theme", next); } catch (e) {}
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
})();
