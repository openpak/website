/* The network map. One engine for the staff map (/admin/network) and the public
   map (/status): hand-rolled SVG, no dependency. Names and descriptions come from
   data, so every string reaches the DOM through textContent. The list view is the
   accessible twin of the map and the layout on phones. */
(function () {
  'use strict';
  var NS = 'http://www.w3.org/2000/svg';

  function svgEl(tag, attrs, parent, text) {
    var e = document.createElementNS(NS, tag);
    for (var k in attrs) if (attrs[k] != null) e.setAttribute(k, attrs[k]);
    if (text != null) e.textContent = text;
    if (parent) parent.appendChild(e);
    return e;
  }
  function el(tag, attrs, parent, text) {
    var e = document.createElement(tag);
    for (var k in attrs || {}) if (attrs[k] != null) e.setAttribute(k, attrs[k]);
    if (text != null) e.textContent = text;
    if (parent) parent.appendChild(e);
    return e;
  }

  var GLYPH = {
    up: 'M5.5 10.5l3 3 6-6.5',
    degraded: 'M10 5.5v5.5M10 14.3v.2',
    down: 'M6.7 6.7l6.6 6.6M13.3 6.7l-6.6 6.6',
    idle: 'M6.5 10h7',
    planned: 'M6.5 10h7',
    unknown: 'M7.9 8.1a2.2 2.2 0 1 1 3.2 1.9c-.7.4-1.1.8-1.1 1.6M10 14.3v.2'
  };
  var DEFAULT_WORDS = { up: 'Working', degraded: 'Degraded', down: 'Down', idle: 'On demand', planned: 'Planned', unknown: 'Not monitored' };
  var KIND_ORDER = { console: 0, adapter: 1, vendor: 2, service: 3, game: 4, core: 5, site: 6, infra: 7, host: 8, group: 3 };
  var KIND_WORD = { console: 'Console network', adapter: 'Network service', vendor: 'Vendor host', service: 'Service', game: 'Game server',
    core: 'Shared core service', site: 'Site / API', infra: 'Infrastructure', host: 'Server', group: 'Group' };
  var FAM = { switch: 1, wiiu: 2, '3ds': 2, wii: 3, ds: 3, ps3: 4, ps4: 4, ps5: 4, xbox360: 5, xbox: 5 };
  var STRINGS = {
    search: 'Search', searchPh: 'Name or what it does', network: 'Network', allNetworks: 'All networks', status: 'Status', all: 'All',
    problems: 'Needs attention', downOnly: 'Down', upOnly: 'Working', map: 'Map', list: 'List', fit: 'Fit to screen', zoomIn: 'Zoom in', zoomOut: 'Zoom out',
    hint: 'Scroll to zoom, drag to move', noMatch: 'Nothing matches.', uses: 'Uses', usedBy: 'Used by', db: 'Database', host: 'Runs on', version: 'Version',
    repo: 'Repository', logs: 'Open logs', networks: 'Networks', close: 'Close', expand: 'Show every member on the map', collapse: 'Fold back into one node',
    members: 'Members', legend: 'Status', loading: 'Loading the map', failed: 'The map could not be loaded.', updated: 'Updated', stage: 'Stage',
    none: 'Nothing', colConsoles: 'Consoles', colUse: 'What they use', colShared: 'Shared by everyone', listShared: 'Shared by every console', listSites: 'Websites', since: 'Running since', update: 'An image update is waiting', shown: 'shown'
  };

  function NetMap(root) {
    this.root = root;
    this.mode = root.dataset.mode === 'public' ? 'public' : 'admin';
    this.url = root.dataset.url;
    var over = {};
    try { over = JSON.parse(root.dataset.strings || '{}'); } catch (e) { }
    this.T = Object.assign({}, STRINGS, over);
    this.words = Object.assign({}, DEFAULT_WORDS, over.words || {});
    this.view = window.matchMedia('(max-width: 720px)').matches ? 'list' : 'map';
    this.f = { net: '', status: '', q: '' };
    this.expanded = {};
    this.sel = null;
    this.t = { k: 1, x: 0, y: 0 };
    this.build();
    this.load(true);
    var self = this;
    setInterval(function () { if (!document.hidden) self.load(false); }, 60000);
  }

  NetMap.prototype.word = function (s) { return this.words[s] || s; };

  NetMap.prototype.icon = function (state, parent, size) {
    var s = svgEl('svg', { viewBox: '0 0 20 20', 'class': 'nm-ico st-' + state, 'aria-hidden': 'true' }, parent);
    if (size) { s.setAttribute('width', size); s.setAttribute('height', size); }
    svgEl('circle', { cx: 10, cy: 10, r: 9 }, s);
    svgEl('path', { d: GLYPH[state] || GLYPH.unknown }, s);
    return s;
  };

  /* ---------- chrome ---------- */
  NetMap.prototype.build = function () {
    var T = this.T, self = this, r = this.root;
    r.textContent = '';
    var bar = el('div', { 'class': 'nm-bar' }, r);
    var ls = el('label', { 'class': 'grow' }, bar); el('span', {}, ls, T.search);
    this.qIn = el('input', { type: 'search', 'class': 'field', placeholder: T.searchPh, autocomplete: 'off', maxlength: 80 }, ls);
    var ln = el('label', {}, bar); el('span', {}, ln, T.network);
    this.netSel = el('select', { 'class': 'field' }, ln);
    var lst = el('label', {}, bar); el('span', {}, lst, T.status);
    this.stSel = el('select', { 'class': 'field' }, lst);
    [['', T.all], ['problems', T.problems], ['down', T.downOnly], ['up', T.upOnly]].forEach(function (o) { el('option', { value: o[0] }, self.stSel, o[1]); });
    var seg = el('div', { 'class': 'nm-seg', role: 'group', 'aria-label': 'View' }, bar);
    this.bMap = el('button', { type: 'button' }, seg, T.map);
    this.bList = el('button', { type: 'button' }, seg, T.list);
    this.legend = el('ul', { 'class': 'nm-legend', 'aria-label': T.legend }, r);
    this.stage = el('div', { 'class': 'nm-stage' }, r);
    this.svg = svgEl('svg', { role: 'img', 'aria-label': 'Network map. The list view has the same content as text.' }, this.stage);
    this.vp = svgEl('g', {}, this.svg);
    this.laneG = svgEl('g', {}, this.vp); this.edgeG = svgEl('g', {}, this.vp); this.nodeG = svgEl('g', {}, this.vp);
    var ctl = el('div', { 'class': 'nm-ctl' }, this.stage);
    this.bIn = el('button', { type: 'button', 'aria-label': T.zoomIn }, ctl, '+');
    this.bOut = el('button', { type: 'button', 'aria-label': T.zoomOut }, ctl, '−');
    this.bFit = el('button', { type: 'button', 'aria-label': T.fit, title: T.fit }, ctl, '⤢');
    el('div', { 'class': 'nm-hint' }, this.stage, T.hint);
    this.panel = el('aside', { 'class': 'nm-panel', 'aria-label': 'Details', tabindex: '-1' }, this.stage);
    this.listEl = el('div', { 'class': 'nm-list' }, r);
    this.notesEl = el('div', { 'class': 'nm-notes' }, r);
    this.stamp = el('p', { 'class': 'nm-notes' }, r);

    this.qIn.addEventListener('input', function () { self.f.q = self.qIn.value.trim().toLowerCase(); self.render(); });
    this.qIn.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { var hit = self.firstHit(); if (hit) { self.select(hit, true); } }
    });
    this.netSel.addEventListener('change', function () { self.f.net = self.netSel.value; self.render(true); });
    this.stSel.addEventListener('change', function () { self.f.status = self.stSel.value; self.render(true); });
    this.bMap.addEventListener('click', function () { self.setView('map'); });
    this.bList.addEventListener('click', function () { self.setView('list'); });
    this.bIn.addEventListener('click', function () { self.zoomBy(1.3); });
    this.bOut.addEventListener('click', function () { self.zoomBy(1 / 1.3); });
    this.bFit.addEventListener('click', function () { self.fit(); });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && self.sel) self.select(null); });
    this.bindPanZoom();
    window.matchMedia('(max-width: 720px)').addEventListener('change', function (e) { self.setView(e.matches ? 'list' : 'map'); });
    this.setView(this.view, true);
  };

  NetMap.prototype.setView = function (v, quiet) {
    this.view = v;
    this.bMap.setAttribute('aria-pressed', v === 'map'); this.bList.setAttribute('aria-pressed', v === 'list');
    this.stage.hidden = v !== 'map'; this.listEl.hidden = v !== 'list';
    if (!quiet) { this.render(true); }
  };

  /* ---------- data ---------- */
  NetMap.prototype.load = function (first) {
    var self = this;
    if (first) this.listEl.textContent = this.T.loading;
    this.root.classList.add('busy');
    fetch(this.url, { credentials: 'same-origin', headers: { Accept: 'application/json' } }).then(function (r) {
      if (!r.ok) throw new Error(r.status); return r.json();
    }).then(function (d) {
      var sig = JSON.stringify([d.nodes.map(function (n) { return [n.id, n.state]; }), d.edges.length]);
      var changed = sig !== self.sig; self.sig = sig;
      self.ingest(d);
      if (first || changed) self.render(first);
      else self.stampText();
    }).catch(function (e) {
      if (first) {
        var sec = self.mode === 'public' && self.root.closest('#netmap-section');
        if (sec) sec.hidden = true; else self.listEl.textContent = self.T.failed;
      }
    }).then(function () { self.root.classList.remove('busy'); });
  };

  NetMap.prototype.ingest = function (d) {
    var self = this, pub = this.mode === 'public';
    this.data = d;
    this.nets = d.networks || [];
    this.netById = {};
    this.nets.forEach(function (n) { self.netById[n.id] = n; });
    this.nodes = d.nodes.map(function (n) {
      var o = Object.assign({}, n);
      o.nets = pub ? (n.net ? [n.net] : []) : (n.networks || []);
      if (o.kind === 'console') o.nets = [n.id.replace(/^console-|^c-/, '')];
      o.fam = o.nets.length ? (FAM[o.nets[0]] || 0) : 0;
      return o;
    });
    this.byId = {}; this.nodes.forEach(function (n) { self.byId[n.id] = n; });
    this.out = {}; this.inn = {};
    d.edges.forEach(function (e) {
      if (!self.byId[e.from] || !self.byId[e.to]) return;
      (self.out[e.from] = self.out[e.from] || []).push(e);
      (self.inn[e.to] = self.inn[e.to] || []).push(e);
    });
    // netSel options
    var cur = this.netSel.value; this.netSel.textContent = '';
    el('option', { value: '' }, this.netSel, this.T.allNetworks);
    this.nets.forEach(function (n) { el('option', { value: n.id }, self.netSel, n.label); });
    this.netSel.value = cur;
    this.drawLegend();
    // Groups (admin): members of games:<net> fold into one node when large.
    this.groups = {};
    if (!pub) this.nodes.forEach(function (n) {
      if (!n.group) return;
      (self.groups[n.group] = self.groups[n.group] || []).push(n);
    });
    this.notes = d.notes || [];
  };

  NetMap.prototype.drawLegend = function () {
    var L = this.legend, self = this; L.textContent = '';
    ['up', 'degraded', 'down', 'idle', 'planned'].forEach(function (s) {
      if (self.mode === 'public' && s === 'idle') return;
      var li = el('li', {}, L); self.icon(s, li); li.appendChild(document.createTextNode(self.word(s)));
    });
    var fams = [[1, 'Switch'], [2, 'Wii U · 3DS'], [3, 'Wii · DS'], [4, 'PlayStation'], [5, 'Xbox']];
    fams.forEach(function (f) {
      var li = el('li', { 'class': 'f' + f[0] }, L); el('span', { 'class': 'nm-sw' }, li); li.appendChild(document.createTextNode(f[1]));
    });
  };

  /* ---------- filtering ---------- */
  NetMap.prototype.matchQ = function (n) {
    var q = this.f.q; if (!q) return false;
    return (n.label || '').toLowerCase().indexOf(q) >= 0 || (n.id || '').toLowerCase().indexOf(q) >= 0 || (n.desc || '').toLowerCase().indexOf(q) >= 0;
  };
  NetMap.prototype.statusOK = function (n) {
    var s = this.f.status; if (!s) return true;
    if (s === 'problems') return n.state === 'degraded' || n.state === 'down';
    return n.state === s;
  };
  NetMap.prototype.firstHit = function () {
    for (var i = 0; i < this.nodes.length; i++) if (this.matchQ(this.nodes[i])) return this.nodes[i].id;
    return null;
  };

  // visible: which real nodes survive the network and status filters.
  NetMap.prototype.computeVisible = function () {
    var self = this, vis = {}, net = this.f.net, pub = this.mode === 'public';
    var lane = {};
    this.nodes.forEach(function (n) {
      if (n.kind === 'console') return;
      if (self.isLaneKind(n)) lane[n.id] = true;
    });
    this.nodes.forEach(function (n) {
      var ok;
      if (n.kind === 'console') ok = !net || n.nets[0] === net;
      else if (lane[n.id]) ok = !net || n.nets.indexOf(net) >= 0;
      else ok = true;
      vis[n.id] = ok && (n.kind === 'console' ? true : self.statusOK(n));
    });
    if (net) {
      // shared nodes stay only when something in the chosen network uses them (or they use it)
      var keep = {};
      this.nodes.forEach(function (n) {
        if (!vis[n.id] || lane[n.id] || n.kind === 'console') return;
        var linked = false;
        (self.inn[n.id] || []).concat(self.out[n.id] || []).forEach(function (e) {
          var o = self.byId[e.from === n.id ? e.to : e.from];
          if (o && (lane[o.id] || o.kind === 'console') && vis[o.id] && o.nets.indexOf(net) >= 0) linked = true;
        });
        if (n.kind === 'host') linked = self.nodes.some(function (o) { return vis[o.id] && lane[o.id] && (o.hosts || []).indexOf(n.hosts && n.hosts[0]) >= 0 && o.nets.indexOf(net) >= 0; });
        keep[n.id] = linked;
      });
      Object.keys(keep).forEach(function (id) { if (!keep[id]) vis[id] = false; });
    }
    if (this.f.status) {
      // a console only stays if something under it matched
      this.nodes.forEach(function (n) {
        if (n.kind !== 'console') return;
        vis[n.id] = self.nodes.some(function (o) { return lane[o.id] && vis[o.id] && o.nets[0] === n.nets[0]; });
      });
    }
    return vis;
  };
  NetMap.prototype.isLaneKind = function (n) {
    return this.mode === 'public' ? n.kind === 'group' : (n.kind === 'adapter' || n.kind === 'game' || n.kind === 'vendor' || n.kind === 'service');
  };

  /* ---------- layout ---------- */
  var W = 176, H = 30, GX = 8, GY = 8;

  // Returns {pos, lanes, labels, rep, items}: rep maps a folded member to its group node.
  NetMap.prototype.layout = function (vis) {
    if (this.mode === 'public') return this.layoutPublic(vis);
    // Lanes wrap into 3 to 8 columns; take the width that fills the stage best, so the
    // map is as large as it can be without scrolling.
    var r = this.svg.getBoundingClientRect(), best = null, bs = -1;
    for (var c = 3; c <= 8; c++) {
      var L = this.layoutAdmin(vis, c); this.L = L;
      var b = this.bounds(), sc = Math.min(r.width / b.w, r.height / b.h);
      if (sc > bs + 1e-6) { bs = sc; best = L; }
    }
    return best;
  };

  NetMap.prototype.layoutAdmin = function (vis, cols) {
    var self = this, pos = {}, lanes = [], labels = [], rep = {}, pseudo = [];
    var COLS = cols || 4, laneX = 200, y = 20;
    var hasQ = !!this.f.q, forceOpen = !!this.f.status;
    // lane membership by primary network
    var members = {};
    this.nodes.forEach(function (n) {
      if (!vis[n.id] || !self.isLaneKind(n)) return;
      var k = n.nets.length ? n.nets[0] : 'unassigned';
      (members[k] = members[k] || []).push(n);
    });
    var order = this.nets.map(function (n) { return n.id; });
    if (members.unassigned) order.push('unassigned');
    order.forEach(function (netId) {
      var net = self.netById[netId] || { id: 'unassigned', label: 'Unassigned', family: 0 };
      var ms = members[netId] || [];
      var cons = self.byId['console-' + netId];
      if (cons && !vis[cons.id]) cons = null;
      if (!ms.length && !cons) return;
      var items = [], games = [];
      ms.sort(function (a, b) { return (KIND_ORDER[a.kind] - KIND_ORDER[b.kind]) || a.label.localeCompare(b.label); });
      ms.forEach(function (n) { if (n.kind === 'game') games.push(n); else items.push(n); });
      if (games.length) {
        var gkey = 'games:' + netId, open = self.expanded[gkey] || forceOpen || games.length <= 6 ||
          (hasQ && games.some(function (g) { return self.matchQ(g); }));
        if (open) games.forEach(function (g) { items.push(g); });
        else {
          var worst = self.worst(games), ps = {
            id: 'grp:' + gkey, label: net.label + ' games', kind: 'group', nets: [netId], fam: net.family, state: worst.state,
            detail: worst.detail, desc: games.length + ' game servers on ' + net.label, members: games, gkey: gkey, pseudo: true
          };
          pseudo.push(ps); self.byId[ps.id] = ps; items.push(ps);
          games.forEach(function (g) { rep[g.id] = ps.id; });
        }
      }
      var rows = Math.max(1, Math.ceil(items.length / COLS));
      var laneH = Math.max(rows * (H + GY) + GY + 14, cons ? 62 : 0);
      var lane = { x: laneX - 10, y: y, w: COLS * (W + GX) + 2, h: laneH, fam: net.family || 0, label: net.label, net: netId };
      lanes.push(lane);
      items.forEach(function (n, i) {
        pos[n.id] = { x: laneX + (i % COLS) * (W + GX), y: y + 16 + Math.floor(i / COLS) * (H + GY), w: W, h: H };
      });
      if (cons) pos[cons.id] = { x: 0, y: y + laneH / 2 - 22, w: 150, h: 44 };
      y += laneH + 16;
    });
    var bottom = y;
    // columns on the right
    var coreX = laneX + COLS * (W + GX) + 150, infX = coreX + W + 150;
    function stack(kinds, x, y0, label) {
      var list = self.nodes.filter(function (n) { return vis[n.id] && kinds.indexOf(n.kind) >= 0; });
      list.sort(function (a, b) { return a.label.localeCompare(b.label); });
      if (!list.length) return y0;
      labels.push({ x: x, y: y0, text: label });
      var yy = y0 + 12;
      list.forEach(function (n) { pos[n.id] = { x: x, y: yy, w: W, h: H }; yy += H + GY; });
      return yy + 10;
    }
    stack(['core'], coreX, 4, 'Shared core services');
    var yy = stack(['site'], infX, 4, 'Sites and API');
    yy = stack(['infra'], infX, yy, 'Infrastructure');
    stack(['host'], infX, yy, 'Servers');
    labels.push({ x: 0, y: 4, text: 'Console networks' });
    labels.push({ x: laneX, y: 4, text: 'Services by network' });
    return { pos: pos, lanes: lanes, labels: labels, rep: rep, pseudo: pseudo };
  };

  NetMap.prototype.layoutPublic = function (vis) {
    var self = this, pos = {}, lanes = [], labels = [], y = 24;
    var gx = 230, gw = 210, gh = 34, cx = gx + gw + 150, cw = 220, ch = 34;
    this.nets.forEach(function (net) {
      var cons = self.byId['c-' + net.id];
      if (!cons || !vis[cons.id]) return;
      var gs = self.nodes.filter(function (n) { return n.kind === 'group' && n.nets[0] === net.id && vis[n.id]; });
      var h = Math.max(54, gs.length * (gh + 8) + 8);
      lanes.push({ x: -8, y: y - 4, w: gx + gw + 16, h: h + 8, fam: net.family, net: net.id });
      pos[cons.id] = { x: 0, y: y + h / 2 - 25, w: 170, h: 50 };
      gs.forEach(function (g, i) { pos[g.id] = { x: gx, y: y + 6 + i * (gh + 8), w: gw, h: gh }; });
      y += h + 22;
    });
    var core = this.nodes.filter(function (n) { return (n.kind === 'core' || n.kind === 'site') && vis[n.id]; });
    core.sort(function (a, b) { return (KIND_ORDER[a.kind] - KIND_ORDER[b.kind]) || a.label.localeCompare(b.label); });
    // spread the shared services over the full height so their lines fan out
    var total = Math.max(y, core.length * (ch + 8));
    var step = core.length > 1 ? Math.max(ch + 8, (total - 40) / core.length) : ch + 8;
    core.forEach(function (n, i) { pos[n.id] = { x: cx, y: 24 + i * step, w: cw, h: ch }; });
    labels.push({ x: 0, y: 8, text: this.T.colConsoles });
    labels.push({ x: gx, y: 8, text: this.T.colUse });
    labels.push({ x: cx, y: 8, text: this.T.colShared });
    return { pos: pos, lanes: lanes, labels: labels, rep: {}, pseudo: [] };
  };

  NetMap.prototype.worst = function (list) {
    var down = 0, bad = 0, n = list.length;
    list.forEach(function (x) { if (x.state === 'down') down++; if (x.state === 'down' || x.state === 'degraded') bad++; });
    if (!bad) return { state: 'up', detail: 'All ' + n + ' running' };
    if (down === n) return { state: 'down', detail: 'None of ' + n + ' running' };
    return { state: 'degraded', detail: bad + ' of ' + n + ' not fully working' };
  };

  /* ---------- rendering ---------- */
  NetMap.prototype.render = function (refit) {
    if (!this.data) return;
    var vis = this.computeVisible();
    this.vis = vis;
    this.stampText();
    this.notesRender();
    if (this.view === 'list') { this.renderList(vis); if (this.sel) this.showPanel(this.sel); return; }
    var L = this.layout(vis), self = this;
    this.L = L;
    this.laneG.textContent = ''; this.edgeG.textContent = ''; this.nodeG.textContent = '';
    this.nodeEls = {}; this.edgeEls = {};
    L.lanes.forEach(function (l) {
      svgEl('rect', { 'class': 'nm-lane f' + l.fam, x: l.x, y: l.y, width: l.w, height: l.h, rx: 10 }, self.laneG);
      if (l.label) svgEl('text', { 'class': 'nm-lane-label', x: l.x + 8, y: l.y + 11 }, self.laneG, l.label);
    });
    L.labels.forEach(function (t) { svgEl('text', { 'class': 'nm-col-label', x: t.x, y: t.y + 8 }, self.laneG, t.text); });
    // edges
    var seen = {}, drawn = 0;
    function rep(id) { return L.rep[id] || id; }
    var order = this.data.edges.slice().sort(function (a, b) { return (a.kind === 'needs') - (b.kind === 'needs'); });
    order.forEach(function (e) {
      var a = rep(e.from), b = rep(e.to);
      if (a === b || !L.pos[a] || !L.pos[b]) return;
      var ka = self.byId[e.from], kb = self.byId[e.to];
      if (!ka || !kb) return;
      var key = a + '>' + b;
      if (seen[key]) return;
      if (e.kind === 'needs' && (seen[b + '>' + a])) return;
      seen[key] = true;
      var p = svgEl('path', { 'class': 'nm-edge ' + e.kind + (e.kind === 'network' ? ' f' + (self.byId[e.from].fam || 0) : ''), d: self.edgePath(L.pos[a], L.pos[b]) }, self.edgeG);
      (self.edgeEls[a] = self.edgeEls[a] || []).push(p); (self.edgeEls[b] = self.edgeEls[b] || []).push(p);
      drawn++;
    });
    // nodes
    var drawNode = function (n) {
      var p = L.pos[n.id]; if (!p) return;
      var g = svgEl('g', { 'class': 'nm-node ' + n.kind + ' st-' + n.state + ' f' + (n.fam || 0) + (self.sel === n.id ? ' sel' : ''),
        transform: 'translate(' + p.x + ',' + p.y + ')', tabindex: 0, role: 'button',
        'aria-label': n.label + ', ' + self.word(n.state) }, self.nodeG);
      svgEl('title', {}, g, n.label + ' — ' + self.word(n.state));
      svgEl('rect', { 'class': 'body', width: p.w, height: p.h, rx: n.kind === 'console' ? 10 : 6 }, g);
      if (n.kind !== 'console') svgEl('rect', { 'class': 'stripe', x: 0, y: 0, width: 4, height: p.h, rx: 2 }, g);
      var room = Math.floor((p.w - (n.kind === 'console' ? 50 : 40)) / (n.kind === 'console' ? 7.6 : 6.3));
      var lab = n.label.length > room ? n.label.slice(0, Math.max(1, room - 1)) + '…' : n.label;
      if (n.kind === 'console') {
        svgEl('text', { x: 14, y: 20 }, g, lab);
        svgEl('text', { 'class': 'sub', x: 14, y: 35 }, g, n.stageLabel || (n.stage ? self.stageWord(n.stage) : ''));
      } else {
        svgEl('text', { x: 13, y: p.h / 2 + 4 }, g, lab);
      }
      var ic = svgEl('g', { transform: 'translate(' + (p.w - 22) + ',' + (p.h / 2 - 7) + ') scale(.7)' }, g);
      svgEl('circle', { cx: 10, cy: 10, r: 9, 'class': 'ic-c' }, ic);
      svgEl('path', { d: GLYPH[n.state] || GLYPH.unknown, 'class': 'ic-g' }, ic);
      g.addEventListener('click', function () { if (self.moved) return; self.select(n.id); });
      g.addEventListener('keydown', function (e) { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); self.select(n.id); } });
      g.addEventListener('pointerenter', function () { self.focusNode(n.id, true); });
      g.addEventListener('pointerleave', function () { self.focusNode(n.id, false); });
      g.addEventListener('focus', function () { self.focusNode(n.id, true); });
      g.addEventListener('blur', function () { self.focusNode(n.id, false); });
      if (self.matchQ(n)) g.classList.add('hit');
      else if (self.f.q) g.classList.add('dim');
      self.nodeEls[n.id] = g;
    };
    this.nodes.forEach(drawNode);
    L.pseudo.forEach(drawNode);
    // Status icon colours come from CSS classes on the node, applied through the shared ico rules.
    this.svg.classList.remove('has-focus');
    if (refit || !this.fitted) { this.fit(); this.fitted = true; } else this.apply();
    this.edgeCount = drawn;
    if (this.sel) this.showPanel(this.sel);
  };

  NetMap.prototype.hostName = function (h) {
    var r = (this.data.hosts || []).filter(function (x) { return x.name === h; })[0];
    return r && r.ip ? h + ' (' + r.ip + ')' : h;
  };

  NetMap.prototype.stageWord = function (s) {
    return { live: 'Live', beta: 'Beta', building: 'In development', planned: 'Planned' }[s] || '';
  };

  NetMap.prototype.edgePath = function (a, b) {
    var x1, y1, x2, y2, dx;
    if (a.x + a.w <= b.x) {
      x1 = a.x + a.w; y1 = a.y + a.h / 2; x2 = b.x; y2 = b.y + b.h / 2; dx = Math.max(40, (x2 - x1) * .5);
      return 'M' + x1 + ' ' + y1 + 'C' + (x1 + dx) + ' ' + y1 + ' ' + (x2 - dx) + ' ' + y2 + ' ' + x2 + ' ' + y2;
    }
    if (b.x + b.w <= a.x) {
      x1 = a.x; y1 = a.y + a.h / 2; x2 = b.x + b.w; y2 = b.y + b.h / 2; dx = Math.max(40, (x1 - x2) * .5);
      return 'M' + x1 + ' ' + y1 + 'C' + (x1 - dx) + ' ' + y1 + ' ' + (x2 + dx) + ' ' + y2 + ' ' + x2 + ' ' + y2;
    }
    x1 = a.x + a.w; y1 = a.y + a.h / 2; x2 = b.x + b.w; y2 = b.y + b.h / 2;
    var bulge = Math.max(x1, x2) + 46 + Math.min(80, Math.abs(y2 - y1) / 6);
    return 'M' + x1 + ' ' + y1 + 'C' + bulge + ' ' + y1 + ' ' + bulge + ' ' + y2 + ' ' + x2 + ' ' + y2;
  };

  NetMap.prototype.focusNode = function (id, on) {
    var self = this, links = {};
    this.svg.classList.toggle('has-focus', on);
    Object.keys(this.edgeEls).forEach(function (k) { self.edgeEls[k].forEach(function (p) { p.classList.remove('on'); }); });
    Object.keys(this.nodeEls).forEach(function (k) { self.nodeEls[k].classList.remove('faded'); });
    if (!on) return;
    (this.edgeEls[id] || []).forEach(function (p) { p.classList.add('on'); });
    var L = this.L;
    this.data.edges.forEach(function (e) {
      var a = L.rep[e.from] || e.from, b = L.rep[e.to] || e.to;
      if (a === id) links[b] = 1; if (b === id) links[a] = 1;
    });
    links[id] = 1;
    // lane members and their console stay lit
    Object.keys(this.nodeEls).forEach(function (k) { if (!links[k]) self.nodeEls[k].classList.add('faded'); });
  };

  /* ---------- pan and zoom ---------- */
  NetMap.prototype.apply = function () {
    this.vp.setAttribute('transform', 'translate(' + this.t.x + ',' + this.t.y + ') scale(' + this.t.k + ')');
  };
  NetMap.prototype.bounds = function () {
    var p = this.L.pos, x0 = 1e9, y0 = 1e9, x1 = -1e9, y1 = -1e9;
    Object.keys(p).forEach(function (k) { var r = p[k]; x0 = Math.min(x0, r.x); y0 = Math.min(y0, r.y); x1 = Math.max(x1, r.x + r.w); y1 = Math.max(y1, r.y + r.h); });
    this.L.lanes.forEach(function (l) { y1 = Math.max(y1, l.y + l.h); x0 = Math.min(x0, l.x); });
    if (x0 > x1) return { x: 0, y: 0, w: 100, h: 100 };
    return { x: x0 - 20, y: y0 - 24, w: x1 - x0 + 40, h: y1 - y0 + 48 };
  };
  NetMap.prototype.fit = function () {
    if (this.view !== 'map' || !this.L) return;
    var b = this.bounds(), r = this.svg.getBoundingClientRect();
    var k = Math.min(r.width / b.w, r.height / b.h, 1.4);
    this.t = { k: k, x: (r.width - b.w * k) / 2 - b.x * k, y: (r.height - b.h * k) / 2 - b.y * k };
    if (b.w * k < r.width * .5 || true) { this.t.y = Math.max(8, this.t.y); }
    this.apply();
  };
  NetMap.prototype.zoomAt = function (f, cx, cy) {
    var k = Math.min(3, Math.max(.12, this.t.k * f)); f = k / this.t.k;
    this.t.x = cx - (cx - this.t.x) * f; this.t.y = cy - (cy - this.t.y) * f; this.t.k = k; this.apply();
  };
  NetMap.prototype.zoomBy = function (f) { var r = this.svg.getBoundingClientRect(); this.zoomAt(f, r.width / 2, r.height / 2); };
  NetMap.prototype.center = function (id) {
    var p = this.L && this.L.pos[this.L.rep[id] || id]; if (!p) return;
    var r = this.svg.getBoundingClientRect(), k = Math.max(this.t.k, .8);
    var shift = r.width > 760 ? -120 : 0;
    this.t = { k: k, x: r.width / 2 + shift - (p.x + p.w / 2) * k, y: r.height / 2 - (p.y + p.h / 2) * k }; this.apply();
  };
  NetMap.prototype.bindPanZoom = function () {
    var self = this, svg = this.svg, ptrs = {}, start = null, pinch = null;
    svg.addEventListener('wheel', function (e) {
      e.preventDefault();
      var r = svg.getBoundingClientRect();
      self.zoomAt(Math.exp(-e.deltaY * (e.ctrlKey ? .01 : .0016)), e.clientX - r.left, e.clientY - r.top);
    }, { passive: false });
    svg.addEventListener('pointerdown', function (e) {
      ptrs[e.pointerId] = { x: e.clientX, y: e.clientY };
      self.moved = false;
      if (Object.keys(ptrs).length === 1) start = { x: e.clientX, y: e.clientY, tx: self.t.x, ty: self.t.y };
      if (Object.keys(ptrs).length === 2) { var a = Object.values(ptrs); pinch = { d: Math.hypot(a[0].x - a[1].x, a[0].y - a[1].y), k: self.t.k }; }
    });
    svg.addEventListener('pointermove', function (e) {
      if (!ptrs[e.pointerId]) return;
      ptrs[e.pointerId] = { x: e.clientX, y: e.clientY };
      var keys = Object.keys(ptrs);
      if (keys.length === 2 && pinch) {
        var a = ptrs[keys[0]], b = ptrs[keys[1]], d = Math.hypot(a.x - b.x, a.y - b.y), r = svg.getBoundingClientRect();
        self.moved = true;
        self.zoomAt(pinch.k * d / pinch.d / self.t.k, (a.x + b.x) / 2 - r.left, (a.y + b.y) / 2 - r.top);
        return;
      }
      if (!start) return;
      var dx = e.clientX - start.x, dy = e.clientY - start.y;
      if (!self.moved && Math.hypot(dx, dy) < 5) return;
      if (!self.moved) { self.moved = true; svg.classList.add('drag'); try { svg.setPointerCapture(e.pointerId); } catch (x) { } }
      self.t.x = start.tx + dx; self.t.y = start.ty + dy; self.apply();
    });
    function end(e) {
      delete ptrs[e.pointerId]; pinch = null; start = Object.keys(ptrs).length ? start : null;
      svg.classList.remove('drag');
      setTimeout(function () { self.moved = false; }, 0);
    }
    svg.addEventListener('pointerup', end); svg.addEventListener('pointercancel', end);
    svg.addEventListener('click', function (e) { if (e.target === svg && !self.moved) self.select(null); });
  };

  /* ---------- selection and panel ---------- */
  NetMap.prototype.select = function (id, center) {
    this.sel = id;
    var sn = id && this.byId[id];
    if (sn && sn.group && !this.expanded[sn.group] && this.view === 'map') { this.expanded[sn.group] = true; this.render(true); }
    Object.keys(this.nodeEls || {}).forEach(function () { });
    if (this.nodeEls) for (var k in this.nodeEls) this.nodeEls[k].classList.toggle('sel', k === id);
    if (id) { this.showPanel(id); if (center && this.view === 'map') this.center(id); } else this.panel.classList.remove('open');
    if (history.replaceState) history.replaceState(null, '', id && id.indexOf('grp:') !== 0 ? '#n=' + encodeURIComponent(id) : location.pathname + location.search);
  };

  NetMap.prototype.chip = function (parent, id, label, state) {
    var self = this, b = el('button', { type: 'button', 'class': 'nm-chip' }, parent);
    this.icon(state, b); b.appendChild(document.createTextNode(label));
    b.addEventListener('click', function () { self.select(id, true); });
    return b;
  };

  NetMap.prototype.showPanel = function (id) {
    var n = this.byId[id], P = this.panel, self = this, T = this.T;
    if (!n) { P.classList.remove('open'); return; }
    P.textContent = '';
    var x = el('button', { type: 'button', 'class': 'close', 'aria-label': T.close }, P, '×');
    x.addEventListener('click', function () { self.select(null); });
    el('h3', {}, P, n.label);
    var k = el('div', { 'class': 'kind' }, P);
    var kindWord = this.mode === 'public' ? { console: 'Console', group: 'Services', core: 'Shared service', site: 'Website' }[n.kind] : (KIND_WORD[n.kind] || n.kind);
    el('span', {}, k, kindWord);
    if (n.stage && this.mode === 'admin') el('span', {}, k, '· ' + this.stageWord(n.stage));
    var st = el('div', { 'class': 'nm-status' }, P);
    this.icon(n.state, st, 20);
    var sb = el('div', {}, st); el('b', {}, sb, this.word(n.state));
    if (n.detail && n.detail !== this.word(n.state)) el('span', {}, sb, n.detail);
    if (n.desc) el('p', {}, P, n.desc);
    var dl = el('dl', {}, P);
    function row(t, v, force) { if (!force && (v == null || v === '' || v.length === 0)) return null; el('dt', {}, dl, t); var dd = el('dd', {}, dl); if (typeof v === 'string') dd.textContent = v; return dd; }
    if (this.mode === 'admin') {
      if (n.nets && n.nets.length && n.kind !== 'console') row(T.networks, n.nets.map(function (i) { return (self.netById[i] || { label: i }).label; }).join(', '));
      row(T.host, (n.hosts || []).map(function (h) { return self.hostName(h); }).join(', '));
      row(T.version, n.version);
      if (n.repo) { var dd = row(T.repo, '', true); var a = el('a', { href: n.repo, rel: 'noopener', target: '_blank' }, dd, n.repo.replace('https://github.com/', '')); }
      if (n.since) row(T.since, n.since.replace('T', ' ').replace('Z', ' UTC'));
      if (n.update) row('Update', T.update);
      if (n.db && n.db.length) row(T.db, n.db.map(function (d) { return d.engine + (d.name ? ' (' + d.name + ')' : ''); }).join(', '));
      if (n.logs) { var d2 = row('Logs', '', true); el('a', { href: '/admin/logs?service=' + encodeURIComponent(n.logs) }, d2, T.logs); }
    }
    // members of a group
    var mem = n.members;
    if (mem && mem.length) {
      el('h4', {}, P, T.members + ' (' + mem.length + ')');
      var ul = el('ul', { 'class': 'nm-members' }, P);
      mem.forEach(function (m) {
        var li = el('li', {}, ul);
        if (self.mode === 'admin') { self.chip(li, m.id, m.label, m.state); }
        else { self.icon(m.state, li); el('span', {}, li, m.label + ' — ' + self.word(m.state)); }
      });
      if (n.pseudo) {
        var eb = el('button', { type: 'button', 'class': 'btn text-sm' }, P, T.expand);
        eb.addEventListener('click', function () { self.expanded[n.gkey] = true; self.sel = n.members[0].id; self.render(); self.center(self.sel); });
      }
    }
    if (n.group && this.mode === 'admin' && this.groups[n.group] && this.groups[n.group].length > 6 && this.expanded[n.group]) {
      var cb = el('button', { type: 'button', 'class': 'btn text-sm' }, P, T.collapse);
      cb.addEventListener('click', function () { delete self.expanded[n.group]; self.sel = null; self.render(); });
    }
    // dependencies
    var outs = this.neighbours(n, 'out'), ins = this.neighbours(n, 'in');
    if (n.kind === 'console' && this.mode === 'admin') {
      var lane = this.nodes.filter(function (o) { return o.nets.indexOf(n.nets[0]) >= 0 && self.isLaneKind(o); });
      if (lane.length) {
        el('h4', {}, P, 'Services (' + lane.length + ')');
        var u3 = el('ul', {}, P);
        lane.slice(0, 40).forEach(function (o) { self.chip(el('li', {}, u3), o.id, o.label, o.state); });
        if (lane.length > 40) el('li', {}, u3, '… and ' + (lane.length - 40) + ' more (List view)');
      }
    }
    if (outs.length) { el('h4', {}, P, T.uses + ' (' + outs.length + ')'); var u1 = el('ul', {}, P); outs.forEach(function (o) { self.chip(el('li', {}, u1), o.id, o.label, o.state); }); }
    if (ins.length) {
      el('h4', {}, P, T.usedBy + ' (' + ins.length + ')');
      var u2 = el('ul', {}, P);
      ins.slice(0, 30).forEach(function (o) { self.chip(el('li', {}, u2), o.id, o.label, o.state); });
      if (ins.length > 30) el('li', {}, u2, '… and ' + (ins.length - 30) + ' more');
    }
    P.classList.add('open');
  };

  // neighbours lists unique linked nodes, skipping console membership and lines to the node's own group.
  NetMap.prototype.neighbours = function (n, dir) {
    var self = this, seen = {}, out = [];
    var list = n.pseudo ? [].concat.apply([], n.members.map(function (m) { return (dir === 'out' ? self.out[m.id] : self.inn[m.id]) || []; }))
      : ((dir === 'out' ? this.out[n.id] : this.inn[n.id]) || []);
    list.forEach(function (e) {
      var o = self.byId[dir === 'out' ? e.to : e.from];
      if (!o || seen[o.id] || o.id === n.id || (n.pseudo && n.members.indexOf(o) >= 0)) return;
      if (e.kind === 'network' && dir === 'in' && o.kind === 'console') return;
      seen[o.id] = 1; out.push(o);
    });
    out.sort(function (a, b) { return (KIND_ORDER[a.kind] - KIND_ORDER[b.kind]) || a.label.localeCompare(b.label); });
    return out;
  };

  /* ---------- list view ---------- */
  NetMap.prototype.renderList = function (vis) {
    var self = this, L = this.listEl, pub = this.mode === 'public';
    L.textContent = '';
    var any = false;
    function section(title, fam, meta, state, items, open) {
      if (!items.length && !state) return;
      any = true;
      var d = el('details', { 'class': 'f' + (fam || 0) }, L);
      if (open) d.setAttribute('open', '');
      var s = el('summary', {}, d);
      if (state) self.icon(state, s);
      el('span', { 'class': 'name' }, s, title);
      el('span', { 'class': 'meta' }, s, meta);
      var ul = el('ul', {}, d);
      items.forEach(function (n) {
        var li = el('li', {}, ul), b = el('button', { type: 'button' }, li);
        self.icon(n.state, b);
        el('span', {}, b, n.label);
        el('span', { 'class': 'sub' }, b, pub ? self.word(n.state) : (KIND_WORD[n.kind] || n.kind) + ' · ' + self.word(n.state));
        b.addEventListener('click', function () { self.select(n.id); });
      });
    }
    var q = !!this.f.q;
    this.nets.forEach(function (net) {
      var cons = self.byId[(pub ? 'c-' : 'console-') + net.id];
      if (!cons || !vis[cons.id]) return;
      var items = self.nodes.filter(function (n) { return vis[n.id] && self.isLaneKind(n) && n.nets[0] === net.id && (!q || self.matchQ(n) || self.matchQ(cons)); });
      items.sort(function (a, b) { return (KIND_ORDER[a.kind] - KIND_ORDER[b.kind]) || a.label.localeCompare(b.label); });
      var meta = items.length + (pub ? '' : '');
      var d0 = { id: cons.id, label: cons.label, state: cons.state, kind: 'console' };
      section(net.label, net.family, (pub ? self.word(cons.state) : cons.detail || ''), cons.state, [d0].concat(items), q && items.length);
    });
    function plain(title, kinds, fam) {
      var items = self.nodes.filter(function (n) { return vis[n.id] && kinds.indexOf(n.kind) >= 0 && (!q || self.matchQ(n)); });
      items.sort(function (a, b) { return a.label.localeCompare(b.label); });
      section(title, fam || 0, items.length + '', null, items, q);
    }
    if (pub) { plain(this.T.listShared, ['core']); plain(this.T.listSites, ['site']); }
    else {
      var un = this.nodes.filter(function (n) { return vis[n.id] && self.isLaneKind(n) && !n.nets.length; });
      if (un.length) section('Unassigned', 0, un.length + '', null, un, true);
      plain('Shared core services', ['core']); plain('Sites and API', ['site']); plain('Infrastructure', ['infra']); plain('Servers', ['host']);
    }
    if (!any) el('div', { 'class': 'nm-empty' }, L, this.T.noMatch);
  };

  NetMap.prototype.stampText = function () {
    if (!this.data) return;
    var g = this.data.generated; var t = new Date(g);
    this.stamp.textContent = this.T.updated + ' ' + (isNaN(t) ? '' : t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })) +
      (this.mode === 'admin' ? ' · ' + this.nodes.length + ' nodes, ' + this.data.edges.length + ' lines' : '');
  };
  NetMap.prototype.notesRender = function () {
    var n = this.notes || [], e = this.notesEl; e.textContent = '';
    if (!n.length || this.mode !== 'admin') return;
    el('strong', {}, e, 'Could not wire');
    var ul = el('ul', {}, e); n.forEach(function (t) { el('li', {}, ul, t); });
  };

  function boot() {
    document.querySelectorAll('[data-netmap]').forEach(function (r) {
      var m = new NetMap(r);
      var h = location.hash.match(/^#n=(.+)$/);
      if (h) { var id = decodeURIComponent(h[1]); var t = setInterval(function () { if (m.data) { clearInterval(t); if (m.byId[id]) m.select(id, true); } }, 150); setTimeout(function () { clearInterval(t); }, 6000); }
      window.__netmap = m;
    });
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot); else boot();
})();
