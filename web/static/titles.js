// Client-side filter for the supported-titles page. The set is small, so
// filtering is a DOM pass rather than a server round-trip. No inline script:
// the CSP is default-src 'self', so this is loaded from /static.
(function () {
  var q = document.getElementById("titles-q");
  var console_ = document.getElementById("titles-console");
  var status = document.getElementById("titles-status");
  var list = document.getElementById("titles-list");
  var empty = document.getElementById("titles-empty");
  var count = document.getElementById("titles-count");
  if (!list) return;
  var rows = Array.prototype.slice.call(list.querySelectorAll(".titles-row"));
  var showing = (count && count.getAttribute("data-showing")) || "Showing";

  function apply() {
    var term = (q.value || "").trim().toLowerCase();
    var c = console_ ? console_.value : "";
    var s = status.value;
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
  [q, console_, status].forEach(function (el) {
    if (!el) return;
    el.addEventListener("input", apply);
    el.addEventListener("change", apply);
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
