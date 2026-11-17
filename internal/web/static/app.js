/* SBOE — comportamiento del front. Todo por delegación de eventos: funciona igual con
   navegación normal y con hx-boost (el body se reemplaza, los listeners de document se quedan). */
(function () {
  'use strict';
  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };
  var mobile = window.matchMedia('(max-width: 1023px)');
  var fold = function (s) { return s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase(); };

  /* ---------- barra de progreso ---------- */
  var active = 0, doneTimer;
  function progressStart() {
    var p = $('#progress'); if (!p) return;
    active++;
    clearTimeout(doneTimer);
    p.classList.remove('done');
    if (!p.classList.contains('on')) { void p.offsetWidth; p.classList.add('on'); }
  }
  function progressDone(force) {
    var p = $('#progress'); if (!p) return;
    active = force ? 0 : Math.max(0, active - 1);
    if (active) return;
    p.classList.add('done');
    doneTimer = setTimeout(function () { p.classList.remove('on', 'done'); }, 350);
  }
  var isPoll = function (elt) { return /every\s/.test((elt && elt.getAttribute && elt.getAttribute('hx-trigger')) || ''); };

  /* ---------- toasts ---------- */
  function toast(msg, kind) {
    var box = $('#toasts'); if (!box) return;
    var t = document.createElement('div');
    t.className = 'toast ' + (kind || 'err');
    t.textContent = msg;
    box.appendChild(t);
    setTimeout(function () { t.classList.add('out'); }, 4200);
    setTimeout(function () { t.remove(); }, 4700);
  }

  /* ---------- botones ocupados ---------- */
  function busy(btn, on) {
    if (!btn) return;
    if (on) { btn.setAttribute('aria-busy', 'true'); setTimeout(function () { btn.disabled = true; }, 0); }
    else { btn.removeAttribute('aria-busy'); btn.disabled = false; }
  }
  var submitBtn = function (f, ev) { return (ev && ev.submitter) || $('button[type=submit], button:not([type])', f); };
  // Formularios normales: si nadie canceló el envío (confirm(), htmx…), progreso + botón ocupado.
  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (e.defaultPrevented || f.id === 'filters' || f.hasAttribute('hx-post') || f.hasAttribute('hx-get')) return;
    progressStart();
    busy(submitBtn(f, e), true);
  });
  window.addEventListener('pageshow', function () { $$('[aria-busy]').forEach(function (b) { busy(b, false); }); progressDone(true); });

  /* ---------- htmx: progreso, errores ---------- */
  document.addEventListener('htmx:beforeRequest', function (e) {
    var f = e.detail.elt;
    if (!isPoll(f)) progressStart();
    if (f && f.tagName === 'FORM' && f.id !== 'filters') {
      f._busyBtn = submitBtn(f, e.detail.requestConfig && e.detail.requestConfig.triggeringEvent);
      busy(f._busyBtn, true);
    }
  });
  document.addEventListener('htmx:afterRequest', function (e) {
    if (!isPoll(e.detail.elt)) progressDone();
    var f = e.detail.elt; if (f && f._busyBtn) { busy(f._busyBtn, false); f._busyBtn = null; }
  });
  document.addEventListener('htmx:responseError', function (e) {
    if (isPoll(e.detail.elt)) return;
    toast(e.detail.xhr.status === 403 ? 'Sesión caducada o sin permiso. Recarga la página.' : 'Algo falló al cargar (' + e.detail.xhr.status + '). Inténtalo de nuevo.');
  });
  document.addEventListener('htmx:sendError', function (e) {
    if (!isPoll(e.detail.elt)) toast('Sin conexión con el servidor.');
  });
  // Navegación normal (enlaces sin htmx): progreso hasta que llegue la página nueva.
  document.addEventListener('click', function (e) {
    var a = e.target.closest('a[href]');
    if (!a || e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    if (a.target === '_blank' || a.hasAttribute('download') || a.hasAttribute('data-download') || a.closest('[hx-boost]') || a.hasAttribute('hx-get')) return;
    if (a.origin === location.origin && a.getAttribute('href').charAt(0) !== '#') progressStart();
  });

  /* ---------- hoja de filtros (móvil) ---------- */
  var lockedY = 0;
  function sheet(open) {
    var b = document.body;
    if (open === b.classList.contains('sheet-open')) return;
    if (open && mobile.matches) {
      lockedY = window.scrollY;
      b.style.top = -lockedY + 'px';
      b.classList.add('sheet-open', 'locked');
    } else if (open) {
      b.classList.add('sheet-open');
    } else {
      var wasLocked = b.classList.contains('locked');
      b.classList.remove('sheet-open', 'locked');
      b.style.top = '';
      if (wasLocked) window.scrollTo(0, lockedY);
    }
  }
  document.addEventListener('click', function (e) {
    var t = e.target.closest('[data-sheet]');
    if (t) sheet(t.dataset.sheet === 'open');
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') sheet(false); });
  mobile.addEventListener('change', function () { sheet(false); });
  document.addEventListener('htmx:beforeSwap', function (e) { if (e.detail.target === document.body) sheet(false); });

  /* ---------- provincias ---------- */
  function provFilter(v) {
    var box = $('#prov-chips'); if (!box) return;
    var n = fold(v.trim()), shown = 0;
    box.classList.toggle('searching', n !== '');
    $$('.chip', box).forEach(function (c) {
      var hit = n === '' || fold(c.dataset.name).indexOf(n) !== -1;
      c.classList.toggle('hidden', !hit);
      if (hit) shown++;
    });
    var none = $('.prov-none'); if (none) none.classList.toggle('hidden', shown > 0);
  }
  // El buscador de provincias no es un filtro: que su "change" no dispare la búsqueda.
  document.addEventListener('change', function (e) { if (e.target.matches('[data-prov-search]')) e.stopPropagation(); }, true);
  document.addEventListener('input', function (e) { if (e.target.matches('[data-prov-search]')) provFilter(e.target.value); });
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-prov-more]'); if (!b) return;
    var box = $('#prov-chips');
    var open = box.classList.toggle('all');
    b.textContent = open ? 'Ver menos' : b.dataset.label;
  });

  /* ---------- contadores y chips de filtros activos ---------- */
  function fieldLabel(el) {
    if (el.name === 'q') return '';
    var l = el.id && $('label[for="' + el.id + '"]');
    if (l) return l.textContent.trim();
    if (el.getAttribute('aria-label')) return el.getAttribute('aria-label');
    var f = el.closest('.field'); return f ? $('label', f).textContent.trim() : el.name;
  }
  function chipText(el) {
    var lab = el.closest('label'); if (!lab) return el.value;
    var span = $('span', lab); if (!span) return lab.textContent.trim();
    var c = span.cloneNode(true); $$('.n', c).forEach(function (n) { n.remove(); });
    return c.textContent.trim();
  }
  function activeFilters(form) {
    var out = [];
    Array.prototype.forEach.call(form.elements, function (el) {
      if (!el.name || el.name === 'sort' || el.name === 'filter_id' || el.disabled) return;
      var text;
      if (el.type === 'hidden') text = el.name === 'asset_type' ? 'Tipo: ' + el.value : el.name === 'status' ? 'Estado: ' + el.value : null;
      else if (el.type === 'checkbox') text = el.checked ? (el.name === 'include_closed' ? 'Incluye no publicadas' : chipText(el)) : null;
      else if (el.type === 'radio') text = el.checked && el.value !== '' ? 'Vivienda habitual: ' + chipText(el) : null;
      else if (el.value.trim() !== '') {
        var l = fieldLabel(el), v = el.value.trim();
        if (el.closest('.money')) v += el.closest('.pct') ? ' %' : ' €';
        text = el.name === 'q' ? '“' + v + '”' : l + ': ' + v;
      }
      if (text) out.push({ el: el, text: text });
    });
    return out;
  }
  function refreshFilters() {
    var form = $('#filters'); if (!form) return;
    var list = activeFilters(form);
    var count = list.filter(function (x) { return x.el.name !== 'q'; }).length;
    var dot = $('#filter-count'); if (dot) dot.textContent = count || '';
    $$('details.fgroup', form).forEach(function (d) {
      var n = list.filter(function (x) { return d.contains(x.el); }).length;
      var c = $('.fcount', d); if (c) c.textContent = n || '';
    });
    var box = $('#active-chips'); if (!box) return;
    box.textContent = '';
    list.forEach(function (x) {
      var b = document.createElement('button');
      b.type = 'button'; b.className = 'achip'; b.textContent = x.text;
      b.setAttribute('aria-label', 'Quitar filtro ' + x.text);
      b.addEventListener('click', function () { clearFilter(x.el); });
      box.appendChild(b);
    });
    if (list.length > 1) {
      var a = document.createElement('a');
      a.href = '/app/subastas'; a.className = 'achip clear'; a.textContent = 'Limpiar todo';
      a.setAttribute('hx-boost', 'true');
      box.appendChild(a);
      if (window.htmx) htmx.process(a);
    }
  }
  function clearFilter(el) {
    if (el.type === 'hidden') { var f = el.form; el.remove(); htmx.trigger(f, 'submit'); refreshFilters(); return; }
    if (el.type === 'checkbox') el.checked = false;
    else if (el.type === 'radio') { var none = el.form.querySelector('input[name="' + el.name + '"][value=""]'); if (none) none.checked = true; }
    else el.value = '';
    if (el.name === 'q' || !el.form || el.form.id !== 'filters') htmx.trigger('#filters', 'submit');
    else el.dispatchEvent(new Event('change', { bubbles: true }));
    refreshFilters();
  }
  var raf;
  function scheduleRefresh() { cancelAnimationFrame(raf); raf = requestAnimationFrame(refreshFilters); }
  document.addEventListener('change', function (e) { if (e.target.form && e.target.form.id === 'filters') scheduleRefresh(); });
  document.addEventListener('input', function (e) { if (e.target.form && e.target.form.id === 'filters') scheduleRefresh(); });

  /* ---------- orden, vista y descarga ---------- */
  function resort(key) {
    var f = $('#sort-field'); if (!f) return;
    f.value = key; htmx.trigger('#filters', 'submit');
  }
  document.addEventListener('change', function (e) { if (e.target.matches('[data-sort-select]')) resort(e.target.value); });
  document.addEventListener('click', function (e) {
    var th = e.target.closest('[data-sort]'); if (th) { resort(th.dataset.sort); return; }
    var v = e.target.closest('.view-toggle [data-view]');
    if (v && !v.classList.contains('on')) {
      document.cookie = 'sboe_view=' + v.dataset.view + '; path=/; max-age=31536000; samesite=lax';
      $$('.view-toggle [data-view]').forEach(function (b) { var on = b === v; b.classList.toggle('on', on); b.setAttribute('aria-pressed', on); });
      var w = $('#results-wrap'); if (w) w.dataset.view = v.dataset.view;
      htmx.trigger('#filters', 'submit');
    }
  });
  document.addEventListener('click', function (e) {
    var a = e.target.closest('a[data-download]'); if (!a || !window.fetch || !window.URL) return;
    e.preventDefault();
    if (a.getAttribute('aria-busy')) return;
    a.setAttribute('aria-busy', 'true'); progressStart();
    fetch(a.href, { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) throw new Error(r.status);
      var cd = r.headers.get('Content-Disposition') || '', m = /filename="([^"]+)"/.exec(cd);
      return r.blob().then(function (b) { return { blob: b, name: m ? m[1] : 'subastas.xlsx' }; });
    }).then(function (x) {
      var url = URL.createObjectURL(x.blob), tmp = document.createElement('a');
      tmp.href = url; tmp.download = x.name; document.body.appendChild(tmp); tmp.click(); tmp.remove();
      setTimeout(function () { URL.revokeObjectURL(url); }, 2000);
      toast('Excel descargado.', 'ok');
    }).catch(function () { toast('No se pudo generar el Excel.'); })
      .finally(function () { a.removeAttribute('aria-busy'); progressDone(); });
  });

  /* ---------- tabla: desplazamiento horizontal ---------- */
  function gridState() {
    var w = $('.grid-wrap'); if (!w) return;
    var max = w.scrollWidth - w.clientWidth;
    var prev = $('[data-gscroll="-1"]'), next = $('[data-gscroll="1"]');
    if (prev) prev.disabled = w.scrollLeft <= 2;
    if (next) next.disabled = w.scrollLeft >= max - 2;
    var tools = $('.grid-tools'); if (tools) tools.classList.toggle('hidden', max <= 2);
    w.classList.toggle('at-start', w.scrollLeft <= 2);
  }
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-gscroll]'); if (!b) return;
    var w = $('.grid-wrap'); if (!w) return;
    var stick = $('.sticky-col', w), visible = w.clientWidth - (stick ? stick.offsetWidth : 0);
    w.scrollBy({ left: +b.dataset.gscroll * Math.max(120, visible * 0.8), behavior: 'smooth' });
  });
  document.addEventListener('scroll', function (e) { if (e.target.classList && e.target.classList.contains('grid-wrap')) gridState(); }, { capture: true, passive: true });
  window.addEventListener('resize', gridState, { passive: true });
  // Con ratón: arrastrar la tabla para moverla (en táctil el scroll nativo ya lo hace).
  var drag = null;
  document.addEventListener('pointerdown', function (e) {
    var w = e.target.closest('.grid-wrap');
    if (!w || e.pointerType !== 'mouse' || e.button !== 0 || e.target.closest('a, button, input, select')) return;
    drag = { w: w, x: e.clientX, y: e.clientY, left: w.scrollLeft, top: w.scrollTop, moved: false };
  });
  document.addEventListener('pointermove', function (e) {
    if (!drag) return;
    var dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    if (!drag.moved && Math.abs(dx) < 4 && Math.abs(dy) < 4) return;
    if (!drag.moved) { drag.moved = true; drag.w.classList.add('dragging'); }
    drag.w.scrollLeft = drag.left - dx; drag.w.scrollTop = drag.top - dy;
    e.preventDefault();
  });
  document.addEventListener('pointerup', function () {
    if (!drag) return;
    var w = drag.w, moved = drag.moved; drag = null;
    w.classList.remove('dragging');
    // evita que soltar tras arrastrar seleccione texto o "haga clic"
    if (moved) { var s = window.getSelection && window.getSelection(); if (s) s.removeAllRanges(); }
  });
  document.addEventListener('keydown', function (e) {
    var w = document.activeElement; if (!w || !w.classList || !w.classList.contains('grid-wrap')) return;
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') { w.scrollBy({ left: (e.key === 'ArrowRight' ? 1 : -1) * 160, behavior: 'smooth' }); e.preventDefault(); }
  });

  /* ---------- tras recibir resultados ---------- */
  document.addEventListener('htmx:afterSwap', function (e) {
    if (e.detail.target.id !== 'results') return;
    var body = $('.results-body', e.detail.target);
    if (body) {
      var t = $('#sheet-total'); if (t) t.textContent = body.dataset.total;
      var w = $('#results-wrap'); if (w) w.dataset.view = body.dataset.view;
    }
    gridState();
    var r = e.detail.target.getBoundingClientRect();
    var fromPager = e.detail.requestConfig && e.detail.requestConfig.elt && e.detail.requestConfig.elt.hasAttribute('data-page');
    // Al paginar, volver al principio de la lista; al filtrar, solo si la lista quedó fuera de la vista.
    if (fromPager || r.bottom < 80) {
      var top = r.top + window.scrollY - (mobile.matches ? 120 : 80);
      if (!document.body.classList.contains('locked')) window.scrollTo({ top: Math.max(0, top), behavior: fromPager ? 'smooth' : 'auto' });
    }
  });

  /* ---------- init por página ---------- */
  function initPage() {
    var form = $('#filters');
    if (form && !form._init) {
      form._init = true;
      var more = $('[data-prov-more]');
      if (more) {
        more.dataset.label = more.textContent;
        if (!$('#prov-chips .chip.more')) more.classList.add('hidden');
      }
      refreshFilters();
    }
    gridState();
    if (!$('#filter-sheet')) { document.body.classList.remove('sheet-open', 'locked'); document.body.style.top = ''; }
    progressDone(true);
  }
  document.addEventListener('DOMContentLoaded', initPage);
  document.addEventListener('htmx:load', function (e) { if (e.detail.elt === document.body || $('#filters', e.detail.elt)) initPage(); });
  document.addEventListener('htmx:historyRestore', initPage);
  if (document.readyState !== 'loading') initPage();
})();
