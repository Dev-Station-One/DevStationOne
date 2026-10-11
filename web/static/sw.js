// DevStationOne service worker: Web Push only (docs/DECISIONS.md ADR-055).
// There is deliberately no fetch handler or cache: every page and API call
// goes to the network, so an upgrade is never masked by a stale offline shell.

self.addEventListener('install', function () { self.skipWaiting(); });
self.addEventListener('activate', function (e) { e.waitUntil(self.clients.claim()); });

self.addEventListener('push', function (event) {
  var data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) { /* non-JSON payload: show the fallback below */ }
  var title = data.title || 'DevStationOne';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (wins) {
      // A visible, focused tab already shows the in-app bell; don't double-alert.
      var focused = wins.some(function (w) { return w.visibilityState === 'visible' && w.focused; });
      if (focused) return;
      return self.registration.showNotification(title, {
        body: data.body || '',
        tag: data.tag || undefined,
        icon: '/assets/logo/pwa-192.png',
        badge: '/assets/logo/favicon-64.png',
        data: { url: data.url || '/projects.html' }
      });
    })
  );
});

self.addEventListener('notificationclick', function (event) {
  event.notification.close();
  var target = new URL((event.notification.data && event.notification.data.url) || '/projects.html', self.location.origin).href;
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (wins) {
      for (var i = 0; i < wins.length; i++) {
        if ('focus' in wins[i]) {
          return wins[i].focus().then(function (w) { return w.navigate ? w.navigate(target) : w; });
        }
      }
      return self.clients.openWindow(target);
    })
  );
});
