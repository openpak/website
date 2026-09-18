// Cloud saves page: filter the game rows, and make a picked version the current one. No inline script: the CSP is default-src 'self'.
(function () {
  var q = document.getElementById("saves-q");
  var consoleSel = document.getElementById("saves-console");
  var list = document.getElementById("saves-list");
  var empty = document.getElementById("saves-empty");
  var count = document.getElementById("saves-count");
  if (!list) return;
  var rows = Array.prototype.slice.call(list.querySelectorAll(".saves-row"));
  var showing = count.getAttribute("data-showing") || "Showing";

  function apply() {
    var term = (q.value || "").trim().toLowerCase();
    var c = consoleSel.value;
    var shown = 0;
    rows.forEach(function (row) {
      var ok = (!term || (row.getAttribute("data-name") || "").toLowerCase().indexOf(term) !== -1) &&
               (!c || row.getAttribute("data-console") === c);
      row.hidden = !ok;
      if (ok) shown++;
    });
    empty.hidden = shown !== 0;
    count.textContent = showing + " " + shown + " / " + rows.length;
  }
  [q, consoleSel].forEach(function (el) {
    el.addEventListener("input", apply);
    el.addEventListener("change", apply);
  });
  apply();

  // Picking a version makes it the current one: the form's own action is restore. The
  // buttons then act on the current version; without script a noscript button submits.
  rows.forEach(function (row) {
    var form = row.querySelector("form");
    form.querySelector("select[name=version]").addEventListener("change", function () { form.submit(); });
    form.addEventListener("submit", function (e) {
      var msg = e.submitter && e.submitter.getAttribute("data-confirm");
      if (msg && !confirm(msg)) e.preventDefault();
    });
  });
})();
