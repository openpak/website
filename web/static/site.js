// Small enhancements on every page. Each one is optional: with scripting off
// the page already works, and these only save a click.
(function () {
  // Copy buttons: data-copy holds the text. Hidden in the markup, so a reader
  // without script never sees a button that does nothing.
  document.querySelectorAll("[data-copy]").forEach(function (b) {
    if (!navigator.clipboard) return;
    b.hidden = false;
    b.addEventListener("click", function () {
      navigator.clipboard.writeText(b.getAttribute("data-copy")).then(function () {
        var label = b.getAttribute("aria-label");
        b.setAttribute("aria-label", b.getAttribute("data-copied") || label);
        b.classList.add("copied");
        setTimeout(function () { b.setAttribute("aria-label", label); b.classList.remove("copied"); }, 1600);
      });
    });
  });
  // The language picker submits as soon as a language is chosen.
  document.querySelectorAll("[data-autosubmit]").forEach(function (b) {
    var sel = b.form && b.form.querySelector("select");
    if (!sel) return;
    b.hidden = true;
    sel.addEventListener("change", function () { b.form.submit(); });
  });
})();
