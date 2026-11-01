// /account/rewards. No inline script: the CSP is default-src 'self', so this is loaded from /static.

// An item's confirm dialog opens from its tile link (#r-<id>), so it can be linked to and reloaded.
// Closing it drops the hash. The same way the titles page opens a title's details.
(function () {
  function sync() {
    var d = /^#r-[A-Za-z0-9_-]+$/.test(location.hash) && document.getElementById(location.hash.slice(1));
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

// A claim, redeem or equip is sent once: the button goes busy while the page changes, so a second
// tap cannot spend twice. (Redeem also carries a key the service charges once.) Coming back with the
// browser's Back button restores the button.
(function () {
  var busy = [];
  Array.prototype.forEach.call(document.querySelectorAll("form[data-once]"), function (f) {
    f.addEventListener("submit", function () {
      var b = f.querySelector("button[type=submit]");
      if (!b || b.disabled) return;
      // After the browser has read the form, or the disabled button would drop out of it.
      setTimeout(function () {
        b.setAttribute("aria-busy", "true");
        b.disabled = true;
        busy.push([b, b.textContent]);
        if (b.getAttribute("data-working")) b.textContent = b.getAttribute("data-working");
      }, 0);
    });
  });
  window.addEventListener("pageshow", function (e) {
    if (!e.persisted) return;
    busy.splice(0).forEach(function (p) { p[0].disabled = false; p[0].removeAttribute("aria-busy"); p[0].textContent = p[1]; });
  });
})();
