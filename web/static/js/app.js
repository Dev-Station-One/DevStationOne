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
      { id: 'activity', href: '/activity.html', icon: 'fa-list-check', label: 'Activity' }
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
      + '</ul></div></div>';
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

  // Notifications bell (docs/IMPLEMENTATION_PLAN.md Slice 15): injected next
  // to the theme toggle so every authenticated page gets it from this one
  // file, the same reasoning as the centralized sidebar above.
  function renderBell() {
    var toggle = document.getElementById('theme-toggle');
    if (!toggle || document.getElementById('notifications-bell')) return;

    var wrap = document.createElement('div');
    wrap.className = 'dropdown';
    wrap.innerHTML =
      '<button id="notifications-bell" class="icon-btn dropdown-toggle position-relative" type="button" data-bs-toggle="dropdown" aria-expanded="false" aria-label="Notifications" title="Notifications">'
      + '<i class="fa-solid fa-bell" aria-hidden="true"></i>'
      + '<span id="notifications-badge" class="badge bg-red position-absolute top-0 start-100 translate-middle rounded-pill" hidden></span>'
      + '</button>'
      + '<div class="dropdown-menu dropdown-menu-end" style="width: 320px; max-height: 400px; overflow-y: auto;">'
      + '<div class="d-flex align-items-center justify-content-between px-3 py-2">'
      + '<span class="fw-bold">Notifications</span>'
      + '<button id="notifications-mark-all" class="btn btn-link btn-sm p-0" type="button">Mark all read</button>'
      + '</div>'
      + '<div id="notifications-list"></div>'
      + '<div id="notifications-empty" class="text-secondary text-center py-3 small" hidden>No notifications</div>'
      + '</div>';
    toggle.insertAdjacentElement('afterend', wrap);

    var bellToggle = wrap.querySelector('.dropdown-toggle');
    var badge = wrap.querySelector('#notifications-badge');
    var listEl = wrap.querySelector('#notifications-list');
    var emptyEl = wrap.querySelector('#notifications-empty');

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
        var item = document.createElement('div');
        item.className = 'dropdown-item-text px-3 py-2 border-bottom' + (n.read_at ? '' : ' bg-body-tertiary');
        item.style.cursor = n.read_at ? 'default' : 'pointer';
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

    wrap.querySelector('#notifications-mark-all').addEventListener('click', function (e) {
      e.stopPropagation();
      var xhr = new XMLHttpRequest();
      xhr.open('POST', '/api/v1/notifications/read-all');
      xhr.onload = function () { loadList(); loadUnreadCount(); };
      xhr.send();
    });

    bellToggle.addEventListener('click', loadList);

    document.addEventListener('ds-ws-event', function (evt) {
      if (!evt.detail || evt.detail.type !== 'notification.created') return;
      loadUnreadCount();
      if (bellToggle.getAttribute('aria-expanded') === 'true') loadList();
    });

    loadUnreadCount();
  }

  document.addEventListener('DOMContentLoaded', function () {
    renderSidebar();
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
      } catch (e) { /* malformed response: keep current theme */ }
    };
    xhr.send();
  });
})();
