/* Keeps /status current: polls /status/live.json and updates the page in place, and switches
   between the overview and the network map. Nothing here is needed to read the page. Names and
   labels reach the DOM through textContent only. */
(function () {
  'use strict';
  var root = document.getElementById('sv-live');
  if (!root) return;
  document.documentElement.classList.add('js');
  var calm = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var ICON = { ok: 'ok', partial: 'warn', down: 'down', idle: 'idle' };
  var BADGE = { ok: 'ok', partial: 'warn', down: 'bad', idle: 'idle' };

  // Numbers ease to their new value; with reduced motion they just change.
  function setNum(el, to) {
    var from = +el.textContent || 0;
    el.setAttribute('data-n', to);
    if (from === to) return;
    if (calm) { el.textContent = to; return; }
    var t0 = performance.now(), dur = 600;
    (function step(now) {
      var p = Math.min(1, (now - t0) / dur);
      el.textContent = Math.round(from + (to - from) * (1 - Math.pow(1 - p, 3)));
      if (p < 1 && +el.getAttribute('data-n') === to) requestAnimationFrame(step);
      else el.textContent = to;
    })(t0);
  }

  function badge(card, state, label) {
    var b = card.querySelector('.sv-badge');
    b.className = 'sv-badge ' + BADGE[state];
    b.querySelector('use').setAttribute('href', '#sv-' + ICON[state]);
    b.querySelector('[data-label]').textContent = label;
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function titleCard(t, strings) {
    var li = el('li', 'sv-card sv-title new'); li.setAttribute('data-id', t.ID);
    var art = el('span', 'sv-art');
    var fb = el('picture', 'sv-fb');
    var src = document.createElement('source');
    src.setAttribute('srcset', '/static/consoles/' + t.Slug + '-dark.svg'); src.setAttribute('media', '(prefers-color-scheme: dark)');
    var mark = document.createElement('img'); mark.src = '/static/consoles/' + t.Slug + '.svg'; mark.alt = ''; mark.width = 40; mark.height = 28;
    fb.appendChild(src); fb.appendChild(mark);
    if (t.Art) {
      var img = document.createElement('img'); img.src = t.Art; img.alt = ''; img.width = 56; img.height = 56; img.setAttribute('data-art', '');
      img.addEventListener('error', function () { img.hidden = true; fb.hidden = false; });
      fb.hidden = true; art.appendChild(img);
    }
    art.appendChild(fb);
    var name = el('span', 'sv-tn'); name.appendChild(el('b', null, t.Name)); name.appendChild(el('small', null, t.Console));
    var p = el('span', 'sv-tp'); var n = el('b', null, t.Players); n.setAttribute('data-n', t.Players);
    p.appendChild(n); p.appendChild(document.createTextNode(' ' + strings));
    li.appendChild(art); li.appendChild(name); li.appendChild(p);
    return li;
  }

  function apply(v) {
    var ban = document.getElementById('sv-banner');
    ban.className = 'sv-banner ' + v.State;
    ban.querySelector('use').setAttribute('href', '#sv-' + (v.State === 'ok' ? 'ok' : 'warn'));
    ban.querySelector('[data-headline]').textContent = v.Headline;
    ban.querySelector('[data-sub]').textContent = v.Sub;
    ban.querySelector('[data-details]').hidden = v.State === 'ok';

    [['online', v.Online], ['playing', v.Playing]].forEach(function (c) {
      var e = root.querySelector('[data-count="' + c[0] + '"]'); if (e) setNum(e, c[1]);
    });

    (v.Networks || []).forEach(function (n) {
      var card = root.querySelector('[data-net="' + n.ID + '"]');
      if (!card) return;
      card.setAttribute('data-state', n.State);
      badge(card, n.State, n.Label);
      var p = card.querySelector('[data-players]');
      p.hidden = !n.Counted;
      if (n.Counted) p.textContent = n.Text;
    });

    var list = document.getElementById('sv-play'), quiet = document.getElementById('sv-quiet');
    if (!list) return;
    var word = (root.querySelector('.sv-tp') && root.querySelector('.sv-tp').lastChild.textContent.trim()) || '';
    var have = {};
    [].slice.call(list.children).forEach(function (li) { have[li.getAttribute('data-id')] = li; });
    var order = [];
    (v.Titles || []).forEach(function (t) {
      var li = have[t.ID];
      if (li) { setNum(li.querySelector('.sv-tp b'), t.Players); delete have[t.ID]; }
      else li = titleCard(t, word || t.Text.replace(/^\d+\s*/, ''));
      order.push(li);
    });
    Object.keys(have).forEach(function (id) { have[id].remove(); });
    order.forEach(function (li, i) { if (list.children[i] !== li) list.insertBefore(li, list.children[i] || null); });
    quiet.hidden = order.length > 0;
    if (!order.length) quiet.textContent = v.Quiet;
  }

  var busy = false;
  function poll() {
    if (busy || document.hidden) return;
    busy = true;
    fetch(root.getAttribute('data-url'), { headers: { Accept: 'application/json' }, credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (v) { if (v) apply(v); })
      .catch(function () {})
      .then(function () { busy = false; });
  }
  setInterval(poll, 30000);
  document.addEventListener('visibilitychange', poll);

  // Cover art that fails to load falls back to the console mark.
  [].forEach.call(root.querySelectorAll('img[data-art]'), function (img) {
    function fb() { img.hidden = true; var f = img.parentNode.querySelector('.sv-fb'); if (f) f.hidden = false; }
    img.addEventListener('error', fb);
    if (img.complete && !img.naturalWidth) fb();
  });

  // Overview / Map. The map script is loaded the first time it is asked for, once its panel is visible.
  var over = document.getElementById('sv-overview'), map = document.getElementById('sv-map'), loaded = false;
  [].forEach.call(root.querySelectorAll('[data-view]'), function (b) {
    b.addEventListener('click', function () {
      var m = b.getAttribute('data-view') === 'map';
      [].forEach.call(root.querySelectorAll('[data-view]'), function (x) { x.setAttribute('aria-pressed', x === b ? 'true' : 'false'); });
      over.hidden = m; map.hidden = !m;
      if (m && !loaded) {
        loaded = true;
        var v = (document.querySelector('link[href*="status.css"]').href.split('?v=')[1] || '');
        var css = document.createElement('link'); css.rel = 'stylesheet'; css.href = '/static/netmap.css?v=' + v; document.head.appendChild(css);
        var js = document.createElement('script'); js.src = '/static/netmap.js?v=' + v; document.body.appendChild(js);
      }
      if (history.replaceState) history.replaceState(null, '', m ? '#map' : location.pathname + location.search);
    });
  });
  if (location.hash === '#map') { var mb = root.querySelector('[data-view="map"]'); if (mb) mb.click(); }
})();
