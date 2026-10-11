// DevStation shared page behavior: account theme preference
// (docs/BOOTSTRAP_COLORS.md, docs/REQUIREMENTS decisions 2026-09-13).
// Theme is account-level (users.theme); localStorage only bridges the
// flash between page paint and the session fetch.

(function () {
  'use strict';

  function stored() {
    try { return window.localStorage.getItem('ds-theme'); } catch (e) { return null; }
  }

  function store(t) {
    try { window.localStorage.setItem('ds-theme', t); } catch (e) { /* private mode: fine */ }
  }

  function apply(theme) {
    document.documentElement.setAttribute('data-bs-theme', theme);
    var btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.setAttribute('aria-label', theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
      btn.title = theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme';
    }
    document.dispatchEvent(new CustomEvent('ds-theme', { detail: { theme: theme } }));
  }

  // Paint before anything else to avoid a flash of the wrong theme.
  apply(stored() === 'light' ? 'light' : 'dark');

  function setTheme(theme, persist) {
    apply(theme);
    store(theme);
    if (!persist) return;
    var xhr = new XMLHttpRequest();
    xhr.open('PUT', '/api/v1/auth/preferences');
    xhr.setRequestHeader('Content-Type', 'application/json');
    xhr.onload = function () {
      if (xhr.status >= 400) {
        // Revert to the previous visual so UI and storage agree.
        var prev = theme === 'dark' ? 'light' : 'dark';
        apply(prev);
        store(prev);
        DS.error('Failed to save theme preference.');
      }
    };
    xhr.send(JSON.stringify({ theme: theme }));
  }

  // Shared sidebar: every page carries <aside id="ds-sidebar" class="navbar
  // navbar-vertical navbar-expand-lg" data-active="...">, this renders the
  // nav content so menu changes stay in one file. The host element keeps the
  // navbar classes in static HTML so Tabler's sibling selectors (e.g.
  // .navbar-vertical ~ .page-wrapper) apply before and after JS runs.
  // Labels use .nav-link-title so Tabler's folded-sidebar CSS can hide them.
  function renderSidebar() {
    var host = document.getElementById('ds-sidebar');
    if (!host) return;
    host.setAttribute('data-bs-theme', 'dark');
    var active = host.getAttribute('data-active') || '';
    var items = [
      { id: 'dashboard', href: '/', icon: 'fa-gauge-high', label: 'Dashboard' },
      { id: 'projects', href: '/projects.html', icon: 'fa-cubes', label: 'Projects' },
      { id: 'runtimes', href: '/runtimes.html', icon: 'fa-server', label: 'Runtimes' }
    ];
    var links = items.map(function (it) {
      return '<li class="nav-item' + (it.id === active ? ' active' : '') + '">'
        + '<a class="nav-link" href="' + it.href + '">'
        + '<i class="fa-solid ' + it.icon + '" aria-hidden="true"></i> '
        + '<span class="nav-link-title">' + it.label + '</span></a></li>';
    }).join('');
    var systemItems = [
      { id: 'activity', href: '/activity.html', icon: 'fa-list-check', label: 'Activity' },
      { id: 'help', href: '/help.html', icon: 'fa-circle-question', label: 'Help' }
    ];
    var systemLinks = systemItems.map(function (it) {
      return '<li class="nav-item' + (it.id === active ? ' active' : '') + '">'
        + '<a class="nav-link" href="' + it.href + '">'
        + '<i class="fa-solid ' + it.icon + '" aria-hidden="true"></i> '
        + '<span class="nav-link-title">' + it.label + '</span></a></li>';
    }).join('');
    host.innerHTML = '<div class="container-fluid">'
      + '<button class="navbar-toggler" type="button" data-bs-toggle="collapse" data-bs-target="#sidebar-menu" aria-controls="sidebar-menu" aria-expanded="false" aria-label="Toggle navigation"><span class="navbar-toggler-icon"></span></button>'
      + '<h1 class="navbar-brand d-flex align-items-center justify-content-center"><a href="/">'
      + '<img src="/assets/logo/devstationone-top-logo.png" alt="DevStationOne" class="ds-logo-full img-fluid">'
      + '</a></h1>'
      + '<div class="collapse navbar-collapse" id="sidebar-menu"><ul class="navbar-nav pt-lg-3">'
      + '<li class="nav-item"><div class="ds-nav-title text-uppercase text-secondary small fw-bold px-3 pt-3 pb-1">Workspace</div></li>'
      + links
      + '<li class="nav-item"><div class="ds-nav-title text-uppercase text-secondary small fw-bold px-3 pt-3 pb-1">System</div></li>'
      + systemLinks
      + '</ul>'
      // Account/theme/notifications cluster, pinned to the rail bottom (former
      // top bar). Rendered as real nav-links (not bare icon buttons) so they
      // share the same hover-expand label treatment as Dashboard/Projects
      // above instead of looking like a bolted-on toolbar. Lives inside
      // #sidebar-menu so the mobile navbar toggler reveals it too, and inside
      // .navbar-vertical so Tabler's folded-hover CSS collapses/reveals it
      // with the rest of the sidebar.
      + '<ul class="navbar-nav mt-auto">'
      + '<li class="nav-item">'
      + '<button class="nav-link" type="button" data-bs-toggle="sidebar-folded" aria-label="Pin sidebar open" title="Pin sidebar open">'
      + '<i class="fa-solid fa-thumbtack" aria-hidden="true"></i><i class="fa-solid fa-thumbtack-slash" aria-hidden="true"></i> <span class="nav-link-title">Pin sidebar</span>'
      + '</button></li>'
      + '<li class="nav-item">'
      + '<button id="theme-toggle" class="nav-link" type="button" aria-label="Switch theme" title="Switch theme">'
      + '<i class="fa-solid fa-sun" aria-hidden="true"></i><i class="fa-solid fa-moon" aria-hidden="true"></i> <span class="nav-link-title">Theme</span>'
      + '</button></li>'
      + '<li class="nav-item dropend" id="notifications-nav-item"></li>'
      + '<li class="nav-item">'
      + '<a class="nav-link" href="/account.html"><i class="fa-solid fa-user-gear" aria-hidden="true"></i> <span class="nav-link-title">Account</span></a>'
      + '</li>'
      + '<li class="nav-item">'
      + '<button id="logout-btn" class="nav-link" type="button"><i class="fa-solid fa-right-from-bracket" aria-hidden="true"></i> <span class="nav-link-title">Log out</span></button>'
      + '</li>'
      + '<li class="nav-item">'
      + '<button id="about-btn" class="nav-link" type="button"><i class="fa-solid fa-circle-info" aria-hidden="true"></i> <span class="nav-link-title">About</span></button>'
      + '</li>'
      + '</ul>'
      + '</div></div>';
    document.getElementById('logout-btn').addEventListener('click', function () {
      var xhr = new XMLHttpRequest();
      xhr.open('POST', '/api/v1/auth/logout');
      xhr.onload = function () { window.location.href = '/login.html'; };
      xhr.onerror = function () { window.location.href = '/login.html'; };
      xhr.send();
    });
    document.getElementById('about-btn').addEventListener('click', function () {
      renderAboutModal();
      // tabler.min.js is a UMD bundle that exports onto window.tabler, not
      // window.bootstrap (its factory target is (globalThis).tabler = {}) —
      // same reasoning as project.html's own modalInstance() helper. Driven
      // by a direct .show() call rather than data-bs-toggle="modal" so a
      // page that also loads the real bootstrap.bundle.min.js (project.html,
      // for Tab) can never double-fire this the way the notifications-bell
      // dropdown did (ADR-034) — Modal's data-api is never touched at all.
      tabler.Modal.getOrCreateInstance(document.getElementById('about-modal')).show();
    });
    // Reflect current fold state on load - Tabler's own sidebar-folded click
    // handler only sets aria-pressed at click time, so a fresh page load
    // needs it set explicitly or the pin icon is wrong until the next click.
    // Tabler's own convention: aria-pressed="true" means folding IS engaged
    // (collapsed), not "pinned" - matched here, and in the CSS below.
    var pinBtn = host.querySelector('[data-bs-toggle="sidebar-folded"]');
    if (pinBtn) {
      pinBtn.setAttribute('aria-pressed', String(document.documentElement.getAttribute('data-bs-sidebar') === 'folded-hover'));
    }
  }

  // About modal: injected once into <body> on first open rather than
  // shipped in every page's static HTML, matching renderSidebar/renderBell
  // above — one shared definition instead of seven copies to keep in sync.
  function renderAboutModal() {
    if (document.getElementById('about-modal')) return;
    var el = document.createElement('div');
    el.innerHTML =
      '<div class="modal fade" id="about-modal" tabindex="-1" aria-hidden="true">'
      + '<div class="modal-dialog modal-dialog-centered">'
      + '<div class="modal-content">'
      + '<div class="modal-body text-center py-4">'
      + '<button type="button" id="about-modal-close" class="icon-btn float-end" aria-label="Close"><i class="fa-solid fa-xmark" aria-hidden="true"></i></button>'
      + '<img src="/assets/devstationone-github-logo.png" alt="" class="mb-3">'
      + '<h3 class="mb-1">DevStationOne</h3>'
      + '<p class="text-secondary mb-3">A self-hosted remote development environment platform.</p>'
      + '<p class="mb-3">Open the Control Server in a browser and operate Development Runtimes — terminals, files, databases, and services — on your own machines from anywhere.</p>'
      + '<p class="text-secondary small mb-0">MIT License &middot; &copy; 2026 <a href="https://briantafoya.com" target="_blank" class="small">Brian Tafoya</a></p>'
      + '<a href="/help.html" class="small">Help</a>'
      + '</div></div></div></div>';
    document.body.appendChild(el.firstElementChild);
    // Plain JS close handler rather than data-bs-dismiss="modal" — the
    // Data API is deliberately avoided everywhere a page might also load
    // the real bootstrap.bundle.min.js (project.html, for Tab), per the
    // dropdown double-firing lesson in ADR-034.
    document.getElementById('about-modal-close').addEventListener('click', function () {
      tabler.Modal.getOrCreateInstance(document.getElementById('about-modal')).hide();
    });
  }

  // Shared live-events connection (docs/IMPLEMENTATION_PLAN.md Slice 14):
  // one WebSocket per page, fanned out to the rest of the page as a
  // 'ds-ws-event' CustomEvent so both the dashboard's own refresh logic and
  // the notifications bell below can react without each opening their own
  // connection.
  // ponytail: fixed 3s reconnect, not exponential backoff — same
  // low-value-background-channel reasoning as the dashboard's original
  // connectEvents (docs/IMPLEMENTATION_PLAN.md Slice 14).
  function connectSharedEvents() {
    var proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    var ws = new WebSocket(proto + '//' + window.location.host + '/api/v1/events/stream');
    ws.onmessage = function (evt) {
      var msg;
      try { msg = JSON.parse(evt.data); } catch (e) { return; }
      document.dispatchEvent(new CustomEvent('ds-ws-event', { detail: msg }));
    };
    ws.onclose = function () { setTimeout(connectSharedEvents, 3000); };
  }

  function escapeText(s) {
    var d = document.createElement('div');
    d.textContent = s == null ? '' : String(s);
    return d.innerHTML;
  }

  // Notifications bell (docs/IMPLEMENTATION_PLAN.md Slice 15): renders into
  // the sidebar's #notifications-nav-item placeholder (see renderSidebar
  // above). .dropend (native Bootstrap) forces the panel to always open to
  // the right of the rail instead of Bootstrap's default up/down auto-flip,
  // which read as broken this close to the viewport edge. List rows reuse
  // this app's existing .list-group pattern (project.html git status /
  // journal attachments) instead of bare dropdown-item-text rows.
  function renderBell() {
    var host = document.getElementById('notifications-nav-item');
    if (!host || host.dataset.rendered) return;
    host.dataset.rendered = '1';
    host.innerHTML =
      '<button id="notifications-bell" class="nav-link dropdown-toggle" type="button" data-bs-toggle="dropdown" aria-expanded="false" aria-label="Notifications" title="Notifications">'
      + '<i class="fa-solid fa-bell" aria-hidden="true"></i> <span class="nav-link-title">Notifications</span>'
      + '<span id="notifications-badge" class="badge bg-red position-absolute top-0 start-100 translate-middle rounded-pill" hidden></span>'
      + '</button>'
      + '<div class="dropdown-menu ds-notifications-panel">'
      + '<div class="d-flex align-items-center justify-content-between px-3 py-2">'
      + '<span class="fw-bold">Notifications</span>'
      + '<button id="notifications-mark-all" class="btn btn-link btn-sm p-0" type="button">Mark all read</button>'
      + '</div>'
      + '<div id="notifications-push" class="px-3 pb-2 small d-flex align-items-center justify-content-between" hidden>'
      + '<span id="notifications-push-label" class="text-secondary"></span>'
      + '<button id="notifications-push-toggle" class="btn btn-sm btn-outline-primary" type="button"></button>'
      + '</div>'
      + '<div id="notifications-list" class="list-group list-group-flush"></div>'
      + '<div id="notifications-empty" class="text-secondary text-center py-4 small" hidden>No notifications</div>'
      + '</div>';

    var bellToggle = host.querySelector('.dropdown-toggle');
    var panel = host.querySelector('.dropdown-menu');
    var badge = host.querySelector('#notifications-badge');
    var listEl = host.querySelector('#notifications-list');
    var emptyEl = host.querySelector('#notifications-empty');

    // #sidebar-menu scrolls internally (overflow:auto, for a long nav list)
    // and clips anything absolutely-positioned trying to escape the rail -
    // the same reason Tabler forces vertical-nav dropdown-menus to
    // position:static in the first place. position:fixed placed from the
    // button's real screen position is the only way out of that clip.
    function positionPanel() {
      var r = bellToggle.getBoundingClientRect();
      // panel is display:none pre-show, so offsetWidth reads 0 - the
      // stylesheet's width is still resolvable via computed style though.
      var panelWidth = parseFloat(getComputedStyle(panel).width) || 352;
      var left = Math.min(r.right + 8, window.innerWidth - panelWidth - 8);
      panel.style.top = r.top + 'px';
      panel.style.left = Math.max(8, left) + 'px';
    }

    function timeAgo(iso) {
      var diff = (Date.now() - new Date(iso).getTime()) / 1000;
      if (diff < 60) return 'just now';
      if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
      if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
      return Math.floor(diff / 86400) + 'd ago';
    }

    function updateBadge(count) {
      if (count > 0) {
        badge.textContent = count > 99 ? '99+' : String(count);
        badge.hidden = false;
      } else {
        badge.hidden = true;
      }
    }

    function loadUnreadCount() {
      var xhr = new XMLHttpRequest();
      xhr.open('GET', '/api/v1/notifications/unread-count');
      xhr.onload = function () {
        if (xhr.status !== 200) return;
        try { updateBadge(JSON.parse(xhr.responseText).unread || 0); } catch (e) { /* malformed: leave badge as-is */ }
      };
      xhr.send();
    }

    function markRead(id) {
      var xhr = new XMLHttpRequest();
      xhr.open('POST', '/api/v1/notifications/' + id + '/read');
      xhr.onload = function () { loadList(); loadUnreadCount(); };
      xhr.send();
    }

    function renderList(items) {
      listEl.innerHTML = '';
      emptyEl.hidden = items.length > 0;
      items.forEach(function (n) {
        var item = document.createElement(n.read_at ? 'div' : 'button');
        if (!n.read_at) item.type = 'button';
        item.className = 'list-group-item py-2' + (n.read_at ? '' : ' list-group-item-action bg-body-tertiary');
        item.innerHTML =
          '<div class="fw-semibold small">' + escapeText(n.title) + '</div>'
          + (n.body ? '<div class="text-secondary small">' + escapeText(n.body) + '</div>' : '')
          + '<div class="text-secondary" style="font-size: .75rem;">' + timeAgo(n.created_at) + '</div>';
        if (!n.read_at) {
          item.addEventListener('click', function () { markRead(n.id); });
        }
        listEl.appendChild(item);
      });
    }

    function loadList() {
      var xhr = new XMLHttpRequest();
      xhr.open('GET', '/api/v1/notifications?limit=20');
      xhr.onload = function () {
        if (xhr.status !== 200) return;
        try { renderList(JSON.parse(xhr.responseText).notifications || []); } catch (e) { /* malformed: leave list as-is */ }
      };
      xhr.send();
    }

    host.querySelector('#notifications-mark-all').addEventListener('click', function (e) {
      e.stopPropagation();
      var xhr = new XMLHttpRequest();
      xhr.open('POST', '/api/v1/notifications/read-all');
      xhr.onload = function () { loadList(); loadUnreadCount(); };
      xhr.send();
    });

    bellToggle.addEventListener('click', function () { positionPanel(); loadList(); });

    document.addEventListener('ds-ws-event', function (evt) {
      if (!evt.detail || evt.detail.type !== 'notification.created') return;
      loadUnreadCount();
      if (bellToggle.getAttribute('aria-expanded') === 'true') loadList();
    });

    initPushToggle(host);
    loadUnreadCount();
  }

  // Web Push opt-in (ADR-055), shown in the bell panel only where the browser
  // supports it. Per-browser: each browser/device subscribes for itself.
  function initPushToggle(host) {
    if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) return;
    var row = host.querySelector('#notifications-push');
    var label = host.querySelector('#notifications-push-label');
    var btn = host.querySelector('#notifications-push-toggle');

    function send(method, path, body, done) {
      var xhr = new XMLHttpRequest();
      xhr.open(method, path);
      if (body) xhr.setRequestHeader('Content-Type', 'application/json');
      xhr.onload = function () { done(xhr.status, xhr.responseText); };
      xhr.onerror = function () { done(0, ''); };
      xhr.send(body ? JSON.stringify(body) : null);
    }

    function b64urlToBytes(s) {
      var pad = '='.repeat((4 - s.length % 4) % 4);
      var raw = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
      var out = new Uint8Array(raw.length);
      for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
      return out;
    }

    function show(text, btnText) {
      label.textContent = text;
      btn.textContent = btnText || '';
      btn.hidden = !btnText;
      btn.disabled = false;
      row.hidden = false;
    }

    function refresh() {
      if (Notification.permission === 'denied') { show('Push blocked in browser settings', null); return; }
      navigator.serviceWorker.register('/sw.js').then(function (reg) {
        return reg.pushManager.getSubscription();
      }).then(function (sub) {
        if (sub) show('Push alerts on for this browser', 'Turn off');
        else show('Get alerts when the tab is closed', 'Turn on');
        btn.dataset.on = sub ? '1' : '';
      }).catch(function () { row.hidden = true; });
    }

    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      btn.disabled = true;
      var reg;
      navigator.serviceWorker.register('/sw.js').then(function () { return navigator.serviceWorker.ready; }).then(function (r) {
        reg = r;
        return reg.pushManager.getSubscription();
      }).then(function (sub) {
        if (btn.dataset.on) {
          if (!sub) return;
          var endpoint = sub.endpoint;
          return sub.unsubscribe().then(function () {
            send('DELETE', '/api/v1/push/subscriptions', { endpoint: endpoint }, function () {});
          });
        }
        return Notification.requestPermission().then(function (perm) {
          if (perm !== 'granted') return;
          return new Promise(function (resolve, reject) {
            send('GET', '/api/v1/push/public-key', null, function (status, text) {
              if (status !== 200) { reject(new Error('push unavailable')); return; }
              resolve(JSON.parse(text).public_key);
            });
          }).then(function (key) {
            return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64urlToBytes(key) });
          }).then(function (sub) {
            var j = sub.toJSON();
            send('POST', '/api/v1/push/subscriptions', { endpoint: j.endpoint, keys: j.keys }, function (status) {
              if (status !== 204) sub.unsubscribe();
              refresh();
            });
          });
        });
      }).then(refresh, function () { show('Could not update push', 'Try again'); });
    });

    refresh();
  }

  // Global "?" shortcut to the help manual (docs/WEB_UI_DESIGN.md). Skipped
  // while typing anywhere text entry could use a literal "?" — form fields,
  // CodeMirror, xterm — and with any modifier held.
  function initHelpShortcut() {
    document.addEventListener('keydown', function (e) {
      if (e.key !== '?' || e.ctrlKey || e.metaKey || e.altKey) return;
      var t = e.target;
      var tag = t && t.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (t && t.isContentEditable)) return;
      if (t && t.closest && (t.closest('.xterm') || t.closest('.CodeMirror'))) return;
      window.location.href = '/help.html';
    });
  }

  document.addEventListener('DOMContentLoaded', function () {
    renderSidebar();
    initHelpShortcut();
    var btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.addEventListener('click', function () {
        var current = document.documentElement.getAttribute('data-bs-theme') === 'light' ? 'light' : 'dark';
        setTheme(current === 'dark' ? 'light' : 'dark', true);
      });
      renderBell();
      connectSharedEvents();
    }
    // The account preference wins over the localStorage bridge once known.
    var xhr = new XMLHttpRequest();
    xhr.open('GET', '/api/v1/auth/session');
    xhr.onload = function () {
      if (xhr.status !== 200) return;
      try {
        var u = JSON.parse(xhr.responseText);
        if (u.theme === 'light' || u.theme === 'dark') {
          if (u.theme !== document.documentElement.getAttribute('data-bs-theme')) {
            apply(u.theme);
          }
          store(u.theme);
        }
        // Users manager link, administrators only (the API enforces the
        // same gate server-side; this only keeps the link out of the way).
        if (u.is_admin) {
          var sysList = document.querySelector('#ds-sidebar .navbar-nav');
          if (sysList) {
            sysList.insertAdjacentHTML('beforeend',
              '<li class="nav-item"><a class="nav-link" href="/users.html">'
              + '<i class="fa-solid fa-users-gear" aria-hidden="true"></i> '
              + '<span class="nav-link-title">Users</span></a></li>');
          }
        }
      } catch (e) { /* malformed response: keep current theme */ }
    };
    xhr.send();
  });
})();
