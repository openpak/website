// Replace unavailable game artwork with the console logo and coming-soon copy.
// Register before checking complete to also catch failures before this script ran.
(function () {
  document.querySelectorAll("[data-title-image]").forEach(function (img) {
    function fallback() {
      img.hidden = true;
      img.parentNode.querySelector(".art-placeholder").hidden = false;
    }
    img.addEventListener("error", fallback);
    if (img.complete && !img.naturalWidth) fallback();
  });
})();

// Client-side filter for the supported-titles page. The set is small, so
// filtering is a DOM pass rather than a server round-trip. No inline script:
// the CSP is default-src 'self', so this is loaded from /static.
(function () {
  var q = document.getElementById("titles-q");
  var list = document.getElementById("titles-list");
  var empty = document.getElementById("titles-empty");
  var count = document.getElementById("titles-count");
  if (!list || !q) return;
  var form = q.form;
  var rows = Array.prototype.slice.call(list.querySelectorAll(".titles-row"));
  var showing = (count && count.getAttribute("data-showing")) || "Showing";
  function picked(name) {
    var el = form.querySelector('input[name="' + name + '"]:checked');
    return el ? el.value : "";
  }

  // A link like /titles?console=Wii arrives with that console already chosen.
  var want = new URLSearchParams(location.search).get("console");
  if (want) {
    form.querySelectorAll('input[name="console"]').forEach(function (el) {
      if (el.value !== want) return;
      el.checked = true;
      // On a phone the chip row scrolls; bring the chosen one into view.
      el.parentNode.scrollIntoView({ block: "nearest", inline: "center" });
    });
  }

  function apply() {
    var term = (q.value || "").trim().toLowerCase();
    var c = picked("console"), s = picked("status");
    var shown = 0;
    rows.forEach(function (row) {
      var name = (row.getAttribute("data-name") || "").toLowerCase();
      var ok = (!term || name.indexOf(term) !== -1) &&
               (!c || row.getAttribute("data-console") === c) &&
               (!s || row.getAttribute("data-status") === s);
      row.hidden = !ok;
      if (ok) shown++;
    });
    if (empty) empty.hidden = shown !== 0;
    if (count) count.textContent = showing + " " + shown + " / " + rows.length;
  }
  form.addEventListener("input", apply);
  form.addEventListener("change", apply);
  form.addEventListener("submit", function (e) { e.preventDefault(); });
  // "/" jumps to the search box, as it does on most sites with one.
  document.addEventListener("keydown", function (e) {
    if (e.key === "/" && !/^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName)) { e.preventDefault(); q.focus(); }
  });
  apply();
})();

// "Request a game" opens a native dialog; the buttons carry data-close so no inline handlers
// are needed under the page's CSP.
(function () {
  var open = document.getElementById("request-open");
  var dialog = document.getElementById("request-dialog");
  if (!open || !dialog || !dialog.showModal) return;
  open.addEventListener("click", function () { dialog.showModal(); });
  Array.prototype.forEach.call(dialog.querySelectorAll("[data-close]"), function (b) {
    b.addEventListener("click", function () { dialog.close(); });
  });
})();

// A title's details open from its row link (#t-<id>), so the dialog can be linked to and a
// compatibility report lands back on it. Closing drops the hash.
(function () {
  function sync() {
    var d = /^#t-\d+$/.test(location.hash) && document.getElementById(location.hash.slice(1));
    if (d && d.showModal && !d.open) d.showModal();
  }
  Array.prototype.forEach.call(document.querySelectorAll("dialog.tdialog"), function (d) {
    d.addEventListener("close", function () {
      if (location.hash === "#" + d.id) history.replaceState(null, "", location.pathname + location.search);
    });
    // A click on the backdrop lands on the dialog itself.
    d.addEventListener("click", function (e) { if (e.target === d) d.close(); });
    Array.prototype.forEach.call(d.querySelectorAll("[data-close]"), function (b) { b.addEventListener("click", function () { d.close(); }); });
  });
  window.addEventListener("hashchange", sync);
  sync();
})();
