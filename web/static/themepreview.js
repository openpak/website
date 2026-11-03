// Try a Rewards theme on, here, without a reload. Loaded in <head> on every page (no defer) so a preview that
// is kept for the session (sessionStorage) is on before the first paint. The tokens come from /themes/<id>, which
// only serves validated colours, and are checked again here before they reach the page.
(function () {
  var KEY = "openpak-theme-preview";
  var root = document.documentElement;
  var dark = window.matchMedia("(prefers-color-scheme: dark)");
  var colour = /^(#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})|rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(,\s*[0-9.]{1,6}\s*)?\))$/i;
  var cur = null, set = [], opener = null;

  function load() { try { return JSON.parse(sessionStorage.getItem(KEY)); } catch (e) { return null; } }
  function save() { try { sessionStorage.setItem(KEY, JSON.stringify(cur)); } catch (e) { /* private mode: the preview lasts this page */ } }
  function forget() { try { sessionStorage.removeItem(KEY); } catch (e) {} }
  function clean(m) {
    var out = {};
    if (m && typeof m === "object") Object.keys(m).forEach(function (k) { if (/^[a-z0-9-]{1,20}$/.test(k) && typeof m[k] === "string" && colour.test(m[k])) out[k] = m[k]; });
    return out;
  }
  function metas() { return Array.prototype.slice.call(document.querySelectorAll('meta[name="theme-color"]')); }

  function paint() {
    set.forEach(function (k) { root.style.removeProperty(k); });
    set = [];
    metas().forEach(function (m) { if (m.dataset.orig !== undefined) m.content = m.dataset.orig; });
    if (!cur) return;
    var tokens = dark.matches ? cur.dark : cur.light;
    Object.keys(tokens).forEach(function (k) { root.style.setProperty("--" + k, tokens[k]); set.push("--" + k); });
    metas().forEach(function (m) {
      var d = /dark/.test(m.media), v = (d ? cur.dark : cur.light).paper;
      if (m.dataset.orig === undefined) m.dataset.orig = m.content;
      if (v) m.content = v;
    });
  }
  if (dark.addEventListener) dark.addEventListener("change", paint);

  cur = load();
  if (cur) { cur.light = clean(cur.light); cur.dark = clean(cur.dark); if (!cur.light.paper || !cur.dark.paper) cur = null; }
  paint();

  function ready(fn) { if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", fn); else fn(); }
  ready(function () {
    var bar = document.getElementById("pvbar");
    if (!bar) return;
    var label = bar.querySelector(".pvname");
    var buy = bar.querySelector('[data-pv="buy"]'), equip = bar.querySelector('[data-pv="equip"]'), stop = bar.querySelector('[data-pv="stop"]');

    function show(msg) {
      document.body.classList.add("previewing");
      bar.hidden = false;
      label.textContent = msg || label.dataset.name.replace("%s", cur.name);
      if (buy) {
        buy.hidden = !(cur && !cur.owned && cur.price);
        if (cur) { buy.querySelector("[name=item]").value = cur.id; buy.querySelector("button").textContent = buy.querySelector("button").dataset.label.replace("%d", cur.price); }
      }
      if (equip) {
        equip.hidden = !(cur && cur.owned && !cur.equipped);
        var eb = equip.querySelector("button");
        eb.textContent = eb.dataset.label;
        if (cur) equip.querySelector("[name=item]").value = cur.id;
      }
    }
    function end(refocus) {
      cur = null; forget(); paint();
      bar.hidden = true;
      document.body.classList.remove("previewing");
      if (refocus && opener && document.contains(opener)) opener.focus();
      opener = null;
    }
    function begin(btn) {
      var d = btn.dataset;
      var item = { id: d.previewId, name: d.previewName || "", price: parseInt(d.previewPrice, 10) || 0, owned: d.previewOwned === "1", equipped: d.previewEquipped === "1" };
      opener = btn;
      var dlg = btn.closest("dialog");
      if (dlg && dlg.open) dlg.close();
      fetch("/themes/" + encodeURIComponent(item.id), { credentials: "same-origin", headers: { Accept: "application/json" } })
        .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
        .then(function (t) {
          item.light = clean(t.light); item.dark = clean(t.dark);
          if (!item.light.paper || !item.dark.paper) throw new Error("bad theme");
          cur = item; save(); paint(); show();
          bar.focus();
        })
        .catch(function () { cur = null; paint(); show(label.dataset.failed); bar.focus(); });
    }

    if (cur) show();
    document.addEventListener("click", function (e) {
      var b = e.target.closest && e.target.closest("[data-preview-id]");
      if (b) { e.preventDefault(); begin(b); }
    });
    stop.addEventListener("click", function () { end(true); });
    // Buying or equipping ends the preview, from the bar or from the shop's own forms.
    document.addEventListener("submit", function (e) {
      var a = e.target.getAttribute && e.target.getAttribute("action");
      if (a === "/account/rewards/redeem" || a === "/account/rewards/equip") forget();
    }, true);
    document.addEventListener("keydown", function (e) {
      if (e.key !== "Escape" || bar.hidden || document.querySelector("dialog[open]")) return;
      end(true);
    });
  });
})();
