// /admin/logs: the "tail the newest lines" switch polls /admin/logs/data for
// lines newer than the newest one on the page and puts them on top.
(function () {
  var box = document.getElementById('logtail');
  var list = document.getElementById('loglines');
  if (!box || !list || list.dataset.live !== '1') { if (box) box.disabled = true; return; }
  var timer = null;
  function poll() {
    var u = new URLSearchParams({ service: list.dataset.service, level: list.dataset.level, q: list.dataset.q, after: list.dataset.newest });
    fetch('/admin/logs/data?' + u, { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (rows) {
        for (var i = rows.length - 1; i >= 0; i--) {
          var r = rows[i], li = document.createElement('li');
          li.className = 'll ' + r.level;
          var t = document.createElement('time'); t.textContent = r.time.slice(0, 8);
          var b = document.createElement('b'); b.textContent = r.level;
          var s = document.createElement('span'); s.textContent = r.msg;
          li.append(t, b, s);
          list.prepend(li);
          list.dataset.newest = r.id;
        }
        while (list.children.length > 1000) list.lastChild.remove();
      })
      .catch(function () {});
  }
  box.addEventListener('change', function () {
    clearInterval(timer);
    if (box.checked) { poll(); timer = setInterval(poll, 3000); }
  });
})();
