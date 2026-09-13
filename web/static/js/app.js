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
        window.alert('Failed to save theme preference.');
      }
    };
    xhr.send(JSON.stringify({ theme: theme }));
  }

  document.addEventListener('DOMContentLoaded', function () {
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
