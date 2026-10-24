// The /turnstile-widget page: hands Turnstile's answer to the app's webview
// through its OpenPakTurnstile JavaScript channel, as one JSON message per event.
(function () {
  function post(kind, token) {
    var ch = window.OpenPakTurnstile;
    if (ch && ch.postMessage) ch.postMessage(JSON.stringify({ kind: kind, token: token || "" }));
  }
  window.openpakTurnstileDone = function (token) { post("token", token); };
  // Returning nothing lets Turnstile retry on its own; the app shows the code if it gives up.
  window.openpakTurnstileError = function (code) { post("error", String(code || "")); };
  window.openpakTurnstileExpired = function () { post("expired"); };
})();
