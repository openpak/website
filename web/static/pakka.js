// The Pakka chat panel. Words come from the page (data-strings); the person is
// the cookie session, and every change carries the page's CSRF token. Without
// script the page says so and nothing else on the site depends on it.
(function () {
  var root = document.getElementById("pakka");
  if (!root) return;
  var S = JSON.parse(root.getAttribute("data-strings") || "{}");
  var api = root.getAttribute("data-api");
  var csrf = root.getAttribute("data-csrf");
  var el = function (id) { return document.getElementById(id); };
  var log = el("pakka-log"), list = el("pakka-list"), empty = el("pakka-empty"), input = el("pakka-input"),
    send = el("pakka-send"), form = el("pakka-form"), errBox = el("pakka-err"), notice = el("pakka-notice"),
    accept = el("pakka-accept"), withdraw = el("pakka-withdraw");
  var state = { consent: false, current: null, busy: false, convs: [], last: "" };

  function call(method, path, body) {
    var h = { "Accept": "application/json" };
    if (method !== "GET") { h["X-CSRF"] = csrf; h["Content-Type"] = "application/json"; }
    return fetch(api + path, { method: method, headers: h, credentials: "same-origin", body: body ? JSON.stringify(body) : undefined });
  }
  function fail(code) {
    var map = { rate_limited: S["pakka.limit"], daily_cap: S["pakka.daily"], busy: S["pakka.busy"], bad_message: S["pakka.too_long"],
      conversation_limit: S["pakka.conversation_limit"], unauthorized: S["pakka.session"], unavailable: S["pakka.unavailable"],
      consent_required: S["pakka.consent_lost"] };
    return map[code] || S["pakka.error"];
  }
  function showErr(msg, retry) {
    errBox.textContent = msg + " ";
    if (retry) {
      var b = document.createElement("button");
      b.type = "button"; b.className = "small retry"; b.textContent = S["pakka.retry"];
      b.addEventListener("click", function () { hideErr(); input.value = state.last; input.focus(); });
      errBox.appendChild(b);
    }
    errBox.hidden = false;
  }
  function hideErr() { errBox.hidden = true; errBox.textContent = ""; }
  function setBusy(b) { state.busy = b; send.disabled = b || !state.consent; input.disabled = b || !state.consent; }
  function setConsent(c) {
    state.consent = c;
    accept.hidden = c; withdraw.hidden = !c;
    notice.open = !c;
    input.disabled = !c || state.busy; send.disabled = !c || state.busy;
  }
  function bubble(role, text) {
    var d = document.createElement("div");
    d.className = "pakka-msg " + (role === "user" ? "me" : "bot");
    var w = document.createElement("span"); w.className = "who"; w.textContent = role === "user" ? S["pakka.you"] : S["pakka.title"];
    var t = document.createElement("span"); t.className = "txt"; t.textContent = text;
    d.appendChild(w); d.appendChild(t); log.appendChild(d); log.scrollTop = log.scrollHeight;
    return t;
  }
  function welcome() { log.textContent = ""; bubble("assistant", S["pakka.welcome"]); }
  function renderList() {
    list.textContent = "";
    empty.hidden = state.convs.length > 0;
    state.convs.forEach(function (c) {
      var li = document.createElement("li"); if (c.id === state.current) li.className = "is-current";
      var pick = document.createElement("button"); pick.type = "button"; pick.className = "pick"; pick.textContent = c.title || S["pakka.untitled"];
      if (c.id === state.current) pick.setAttribute("aria-current", "true");
      pick.addEventListener("click", function () { open(c.id); });
      var del = document.createElement("button"); del.type = "button"; del.className = "del";
      del.textContent = "×"; del.setAttribute("aria-label", S["pakka.delete"] + ": " + (c.title || S["pakka.untitled"])); del.title = S["pakka.delete"];
      del.addEventListener("click", function () { remove(c.id); });
      li.appendChild(pick); li.appendChild(del); list.appendChild(li);
    });
  }
  function refresh() {
    return call("GET", "/conversations").then(function (r) { return r.json(); }).then(function (j) { state.convs = j.conversations || []; renderList(); });
  }
  function open(id) {
    hideErr();
    return call("GET", "/conversations/" + id).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (c) {
      state.current = c.id; welcome();
      (c.messages || []).forEach(function (m) { bubble(m.role, m.text); });
      renderList(); input.focus();
    }).catch(function () { showErr(S["pakka.error"], false); });
  }
  function remove(id) {
    if (!window.confirm(S["pakka.delete.confirm"])) return;
    call("DELETE", "/conversations/" + id).then(function () {
      if (state.current === id) { state.current = null; welcome(); }
      return refresh();
    }).then(function () { announce(S["pakka.deleted"]); });
  }
  function announce(t) { var d = document.createElement("span"); d.className = "sr-only"; d.setAttribute("role", "status"); d.textContent = t; root.appendChild(d); setTimeout(function () { d.remove(); }, 2000); }
  function ensureConversation() {
    if (state.current) return Promise.resolve(state.current);
    return call("POST", "/conversations").then(function (r) {
      return r.json().then(function (j) { if (!r.ok) { var e = new Error(j.code); e.code = j.code; throw e; } state.current = j.id; return j.id; });
    });
  }
  function typing(label) {
    var t = document.querySelector(".pakka-typing");
    if (!t) {
      t = document.createElement("div"); t.className = "pakka-typing";
      t.innerHTML = '<span class="pakka-dots" aria-hidden="true"><i></i><i></i><i></i></span><span class="lbl"></span>';
      log.appendChild(t);
    }
    t.querySelector(".lbl").textContent = label; log.scrollTop = log.scrollHeight;
  }
  function untype() { var t = document.querySelector(".pakka-typing"); if (t) t.remove(); }

  function sendMessage(text) {
    hideErr(); state.last = text; setBusy(true);
    bubble("user", text); typing(S["pakka.typing"]);
    var out = null;
    ensureConversation().then(function (id) {
      return fetch(api + "/conversations/" + id + "/messages", { method: "POST", credentials: "same-origin",
        headers: { "Content-Type": "application/json", "X-CSRF": csrf, "Accept": "text/event-stream" }, body: JSON.stringify({ text: text }) });
    }).then(function (r) {
      if (!r.ok) return r.json().then(function (j) { var e = new Error(j.code); e.code = j.code; throw e; });
      var reader = r.body.getReader(), dec = new TextDecoder(), buf = "";
      function handle(block) {
        var ev = "message", data = "";
        block.split("\n").forEach(function (l) { if (l.indexOf("event: ") === 0) ev = l.slice(7); else if (l.indexOf("data: ") === 0) data += l.slice(6); });
        var j = {}; try { j = JSON.parse(data || "{}"); } catch (e) {}
        if (ev === "tool") typing(S["pakka.looking"]);
        else if (ev === "delta") { untype(); if (!out) out = bubble("assistant", ""); out.textContent += j.text; log.scrollTop = log.scrollHeight; }
        else if (ev === "error") { var e = new Error(j.code); e.code = j.code; throw e; }
      }
      function pump() {
        return reader.read().then(function (res) {
          if (res.done) return;
          buf += dec.decode(res.value, { stream: true });
          var parts = buf.split("\n\n"); buf = parts.pop();
          parts.forEach(handle);
          return pump();
        });
      }
      return pump();
    }).then(function () { untype(); if (!out) throw new Error("empty"); return refresh(); })
      .catch(function (e) { untype(); if (out && out.parentNode) out.parentNode.remove(); showErr(fail(e && e.code), !e || e.code === undefined || e.code === "model_error"); })
      .then(function () { setBusy(false); if (state.consent) input.focus(); });
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var text = input.value.trim();
    if (!text || state.busy || !state.consent) return;
    input.value = ""; sendMessage(text);
  });
  input.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); form.requestSubmit(); }
  });
  el("pakka-new").addEventListener("click", function () { state.current = null; welcome(); renderList(); hideErr(); input.focus(); });
  accept.addEventListener("click", function () {
    call("POST", "/consent").then(function (r) { if (!r.ok) throw 0; setConsent(true); announce(S["pakka.notice.accepted"]); input.focus(); })
      .catch(function () { showErr(S["pakka.error"], false); });
  });
  withdraw.addEventListener("click", function () { call("DELETE", "/consent").then(function () { setConsent(false); }); });

  welcome();
  call("GET", "/state").then(function (r) { return r.json(); }).then(function (s) {
    if (s.eligible === false || s.available === false) { showErr(S["pakka.unavailable"], false); setConsent(false); accept.hidden = true; return; }
    setConsent(!!s.consent);
    return refresh();
  }).catch(function () { showErr(S["pakka.unavailable"], false); });
})();
