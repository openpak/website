/* Pill and tab rows turn into one native <select> when they do not fit on one line.
 * Mark a row with data-pillselect. The row stays the source of truth: the select is built from its
 * links, buttons or radio labels, picks the same destination, and shows the current choice (counts
 * in brackets). Without scripting, or when the row fits, nothing changes: the pills stay. */
(function () {
  'use strict';
  var rows = [];
  var COUNT = /(^|\s)(count|ct|num)(\s|$)/;

  function items(row) {
    return Array.prototype.filter.call(row.querySelectorAll('a,button,label'), function (el) {
      return !el.closest('.ps-holder') && !el.hidden;
    });
  }
  function text(el) {
    var s = '';
    (function walk(n) {
      n.childNodes.forEach(function (c) {
        if (c.nodeType === 3) { s += c.textContent; return; }
        if (c.nodeType !== 1 || c.getAttribute('aria-hidden') === 'true' || c.tagName === 'INPUT') { return; }
        if (COUNT.test(c.className) && c.textContent.trim()) { s += ' (' + c.textContent.trim() + ') '; return; }
        walk(c);
        s += ' ';
      });
    })(el);
    return s.replace(/\s+/g, ' ').replace(/ \)/g, ')').trim();
  }
  function current(el) {
    var i = el.tagName === 'LABEL' ? el.querySelector('input') : null;
    if (i) { return i.checked; }
    var c = el.getAttribute('aria-current');
    return (c !== null && c !== 'false') || el.getAttribute('aria-selected') === 'true' ||
      el.getAttribute('aria-pressed') === 'true' || /(^|\s)(active|on|is-active|primary)(\s|$)/.test(el.className);
  }
  function overflows(row) {
    var its = items(row), top = null, wrapped = false;
    its.forEach(function (el) {
      if (!el.offsetParent) { return; }
      if (top === null) { top = el.offsetTop; } else if (Math.abs(el.offsetTop - top) > 4) { wrapped = true; }
    });
    var p = row.parentElement;
    return wrapped || row.scrollWidth > row.clientWidth + 1 ||
      row.getBoundingClientRect().right > document.documentElement.clientWidth + 1 ||
      (p && row.offsetWidth > p.clientWidth + 1);
  }
  function build(row, st) {
    var its = items(row);
    st.items = its;
    st.sel.textContent = '';
    its.forEach(function (el, i) {
      var o = document.createElement('option');
      o.value = String(i);
      o.textContent = text(el);
      o.selected = current(el);
      st.sel.appendChild(o);
    });
  }
  function fit(st) {
    var row = st.row;
    if (!row.isConnected) { return; }
    row.classList.remove('ps-on');
    if (!row.offsetWidth && !row.offsetHeight) { return; }
    build(row, st);
    if (st.items.length > 1 && overflows(row)) { row.classList.add('ps-on'); }
  }
  function enhance(row) {
    if (row.__ps) { return; }
    var sel = document.createElement('select');
    sel.className = 'pillselect field';
    var label = row.getAttribute('aria-label') || row.getAttribute('data-pillselect');
    if (label) { sel.setAttribute('aria-label', label); }
    var holder = sel;
    if (row.tagName === 'UL' || row.tagName === 'OL') {
      holder = document.createElement('li');
      holder.appendChild(sel);
    }
    holder.classList.add('ps-holder');
    row.appendChild(holder);
    var st = { row: row, sel: sel, items: [] };
    row.__ps = st;
    rows.push(st);
    sel.addEventListener('change', function () {
      var el = st.items[+sel.value];
      if (!el) { return; }
      if (el.tagName === 'A') { location.href = el.href; return; }
      var i = el.tagName === 'LABEL' ? el.querySelector('input') : null;
      (i || el).click();
    });
    new MutationObserver(function () {
      if (!row.classList.contains('ps-on')) { return; }
      build(row, st);
    }).observe(row, { attributes: true, subtree: true, attributeFilter: ['aria-current', 'aria-selected', 'aria-pressed', 'class', 'checked'] });
    row.addEventListener('change', function () { build(row, st); });
    fit(st);
  }
  function scan() {
    document.querySelectorAll('[data-pillselect]').forEach(enhance);
    rows.forEach(fit);
  }
  var queued = false;
  function later() {
    if (queued) { return; }
    queued = true;
    requestAnimationFrame(function () { queued = false; scan(); });
  }
  window.addEventListener('resize', later);
  window.addEventListener('load', later);
  window.addEventListener('pageshow', later);
  if (document.fonts && document.fonts.ready) { document.fonts.ready.then(later); }
  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', scan); } else { scan(); }
})();
