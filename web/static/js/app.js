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
    host.innerHTML = '<div class="container-fluid">'
      + '<button class="navbar-toggler" type="button" data-bs-toggle="collapse" data-bs-target="#sidebar-menu" aria-controls="sidebar-menu" aria-expanded="false" aria-label="Toggle navigation"><span class="navbar-toggler-icon"></span></button>'
      + '<h1 class="navbar-brand d-flex align-items-center"><a href="/">'
      + '<img src="/assets/logo/devstationone-top-logo.png" alt="DevStationOne" class="ds-logo-full img-fluid">'
      + '</a></h1>'
      + '<div class="collapse navbar-collapse" id="sidebar-menu"><ul class="navbar-nav pt-lg-3">'
      + '<li class="nav-item"><div class="ds-nav-title text-uppercase text-secondary small fw-bold px-3 pt-3 pb-1">Workspace</div></li>'
      + links
      + '</ul></div></div>';
  }

  document.addEventListener('DOMContentLoaded', function () {
    renderSidebar();
    var btn = document.getElementById('theme-toggle');
    if (btn) {
      btn.addEventListener('click', function () {
        var current = document.documentElement.getAttribute('data-bs-theme') === 'light' ? 'light' : 'dark';
        setTheme(current === 'dark' ? 'light' : 'dark', true);
      });
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
