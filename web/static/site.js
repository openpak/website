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
  // The add-friend dialog: the field follows the chosen kind of code, and
  // types the dashes and prefix itself, so what is sent is always well formed.
  document.querySelectorAll("[data-friend-form]").forEach(function (form) {
    var kind = form.querySelector("select[name=kind]");
    var input = form.querySelector("input[name=friend_code]");
    var shapes = {
      openpak: { prefix: "OP-", groups: [4, 4], keep: /[^2-9A-HJKMNP-TV-Z]/g, pattern: "OP-[2-9A-HJKMNP-TV-Z]{4}-[2-9A-HJKMNP-TV-Z]{4}", mode: "text" },
      switch: { prefix: "SW-", groups: [4, 4, 4, 4], keep: /[^0-9A-F]/g, pattern: "SW-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}(-[0-9A-F]{4})?", mode: "text" },
      "3ds": { prefix: "", groups: [4, 4, 4], keep: /[^0-9]/g, pattern: "[0-9]{4}-[0-9]{4}-[0-9]{4}", mode: "numeric" }
    };
    function shape() { return shapes[kind.value] || shapes.openpak; }
    function fit() {
      var sh = shape();
      var raw = input.value.toUpperCase();
      if (sh.prefix && raw.indexOf(sh.prefix.slice(0, 2)) === 0) raw = raw.slice(2);
      raw = raw.replace(sh.keep, "");
      var parts = [], at = 0;
      sh.groups.forEach(function (n) { if (at < raw.length) parts.push(raw.slice(at, at + n)); at += n; });
      input.value = parts.length ? sh.prefix + parts.join("-") : "";
    }
    function onKind() {
      var sh = shape();
      input.pattern = sh.pattern;
      input.inputMode = sh.mode;
      input.placeholder = kind.options[kind.selectedIndex].getAttribute("data-placeholder") || "";
      fit();
    }
    kind.addEventListener("change", onKind);
    input.addEventListener("input", fit);
    onKind();
  });
  // Opening a :target modal moves focus into it, so a keyboard lands on the form.
  function focusTarget() {
    var m = location.hash && document.querySelector(".modal" + location.hash);
    var f = m && m.querySelector("select, input:not([type=hidden])");
    if (f) f.focus();
  }
  window.addEventListener("hashchange", focusTarget);
  focusTarget();
  // The language picker submits as soon as a language is chosen.
  document.querySelectorAll("[data-autosubmit]").forEach(function (b) {
    var sel = b.form && b.form.querySelector("select");
    if (!sel) return;
    b.hidden = true;
    sel.addEventListener("change", function () { b.form.submit(); });
  });
})();
