// DevStation project file tree (Code tab sidebar). A lazy-loading tree of
// the Project's working directory over the files REST API, with a CRUD
// context menu (new file / new folder / rename / delete). The tree logic
// mirrors dialogs.js's directory picker (which runs pre-Project over the
// runtime browse endpoints, so it can't call this directly) — deliberately
// separate: the two roots and entry types differ. ponytail: duplicated lazy
// expand/cache pattern; unify when a third tree consumer appears.
//
// API: DS.fileTree.create($container, { projectID, onOpenFile })
//   returns { refresh(), invalidate(path) }.
(function () {
  'use strict';

  function joinPath(parent, name) {
    return parent === '.' ? name : parent + '/' + name;
  }

  function parentPath(p) {
    var i = p.lastIndexOf('/');
    return i === -1 ? '.' : p.slice(0, i);
  }

  window.DS = window.DS || {};
  window.DS.fileTree = {
    create: function ($container, opts) {
      var projectID = opts.projectID;
      var onOpenFile = opts.onOpenFile || function () {};

      var cache = {}; // path -> { loaded, expanded, entries: [FileInfo] }

      function baseURL() {
        return '/api/v1/projects/' + projectID + '/files';
      }

      function loadDir(path) {
        return $.get(baseURL(), { path: path }).then(function (entries) {
          entries.sort(function (a, b) {
            if (a.type === 'dir' && b.type !== 'dir') return -1;
            if (b.type === 'dir' && a.type !== 'dir') return 1;
            return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
          });
          cache[path] = { loaded: true, expanded: true, entries: entries };
          return cache[path];
        });
      }

      // invalidate marks a directory stale (its parent too, for renames).
      function invalidate(path) {
        if (path === '.') { cache = {}; return; }
        if (cache[path]) cache[path].loaded = false;
        var parent = parentPath(path);
        if (cache[parent]) cache[parent].loaded = false;
      }

      function refresh() {
        cache = {};
        render();
      }

      // --- rendering ---

      function render() {
        var scrollTop = $container.scrollTop();
        $container.empty();
        renderDir($container, '.', 0);
        $container.scrollTop(scrollTop);
      }

      function renderDir($c, path, depth) {
        var node = cache[path];
        if (!node || !node.loaded) {
          if (!node) cache[path] = { loaded: false, expanded: path === '.', entries: [] };
          loadDir(path).then(render);
          $c.append($('<div class="text-secondary small">').css('padding-left', (depth * 1.1 + 0.4) + 'rem').text('Loading…'));
          return;
        }
        node.entries.forEach(function (e) {
          renderEntry($c, path, depth, e);
        });
        if (!node.entries.length) {
          $c.append($('<div class="text-secondary small">').css('padding-left', (depth * 1.1 + 0.4) + 'rem').text('(empty)'));
        }
      }

      function iconFor(entry) {
        if (entry.type === 'dir') return 'fa-folder text-secondary';
        var n = entry.name.toLowerCase();
        var i = n.lastIndexOf('.');
        var ext = i === -1 ? '' : n.slice(i + 1);
        var map = {
          js: 'fa-file-code', ts: 'fa-file-code', jsx: 'fa-file-code', tsx: 'fa-file-code',
          go: 'fa-file-code', py: 'fa-file-code', rb: 'fa-file-code', rs: 'fa-file-code',
          php: 'fa-file-code', java: 'fa-file-code', c: 'fa-file-code', h: 'fa-file-code',
          html: 'fa-file-code', css: 'fa-file-code', sql: 'fa-database', md: 'fa-file-lines',
          json: 'fa-file-code', yml: 'fa-file-code', yaml: 'fa-file-code', toml: 'fa-file-code',
          sh: 'fa-terminal', txt: 'fa-file-lines'
        };
        return 'fa-' + (map[ext] || 'fa-file');
      }

      function renderEntry($c, parent, depth, entry) {
        var path = joinPath(parent, entry.name);
        var isDir = entry.type === 'dir';
        var node = cache[path];
        var expanded = !!(node && node.expanded);
        var $row = $('<div class="d-flex align-items-center gap-1 rounded filetree-row">')
          .css({ 'padding-left': (depth * 1.1 + 0.2) + 'rem', cursor: 'pointer', 'padding-block': '2px' });
        $row.attr('data-tree-path', path).attr('data-tree-type', entry.type);
        if (isDir) {
          $row.append(
            $('<span class="text-secondary flex-shrink-0 d-inline-flex justify-content-center" style="width:1rem">')
              .html('<i class="fa-solid fa-chevron-' + (expanded ? 'down' : 'right') + '" aria-hidden="true"></i>')
          );
        } else {
          $row.append('<span class="flex-shrink-0 d-inline-flex justify-content-center" style="width:1rem"></span>');
        }
        $row.append(
          $('<span class="text-truncate" title="' + path + '">')
            .append('<i class="fa-solid ' + iconFor(entry) + ' me-1 ' + (isDir ? 'text-secondary' : 'text-primary') + '" aria-hidden="true"></i>')
            .append(document.createTextNode(entry.name))
        );
        $row.on('click', function () {
          if (isDir) {
            if (cache[path]) cache[path].expanded = !cache[path].expanded;
            else cache[path] = { loaded: false, expanded: true, entries: [] };
            render();
          } else {
            onOpenFile(path);
          }
        });
        $row.on('contextmenu', function (e) {
          e.preventDefault();
          e.stopPropagation();
          openMenuFor(parent, entry, e.pageX, e.pageY);
        });
        $c.append($row);
        if (isDir && expanded) renderDir($c, path, depth + 1);
      }

      // --- context menu ---

      var $menu = null;
      function closeMenu() {
        if ($menu) { $menu.remove(); $menu = null; }
        $(document).off('click.filetree-menu');
      }

      function menuItem(label, icon, cls, fn) {
        return $('<button type="button" class="dropdown-item ' + (cls || '') + '">')
          .html('<i class="fa-solid ' + icon + ' me-2" aria-hidden="true"></i>' + label)
          .on('click', function () { closeMenu(); fn(); });
      }

      function openMenu(x, y, items) {
        closeMenu();
        $menu = $('<div class="dropdown-menu show" role="menu" style="position:fixed;z-index:2000;min-width:11rem"></div>');
        items.forEach(function (it) {
          if (it) $menu.append(it);
        });
        $(document.body).append($menu);
        var w = $menu.outerWidth() || 180, h = $menu.outerHeight() || 100;
        if (x + w > window.innerWidth - 8) x = window.innerWidth - w - 8;
        if (y + h > window.innerHeight - 8) y = window.innerHeight - h - 8;
        $menu.css({ left: x, top: y });
        $(document).on('click.filetree-menu', closeMenu);
      }

      function promptName(title, initial) {
        return DS.prompt({ title: title, value: initial || '' });
      }

      function newFile(parent) {
        promptName('New file in ' + (parent === '.' ? 'project root' : parent)).then(function (name) {
          if (!name) return;
          var newPath = joinPath(parent, name);
          $.ajax({
            url: baseURL() + '/create',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ path: newPath })
          }).done(function () {
            invalidate(newPath);
            render();
            onOpenFile(newPath);
          }).fail(function (xhr) {
            DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to create file.');
          });
        });
      }

      function newFolder(parent) {
        promptName('New folder in ' + (parent === '.' ? 'project root' : parent)).then(function (name) {
          if (!name) return;
          var newPath = joinPath(parent, name);
          $.ajax({
            url: baseURL() + '/mkdir',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ path: newPath })
          }).done(function () {
            invalidate(newPath);
            render();
          }).fail(function (xhr) {
            DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to create folder.');
          });
        });
      }

      function rename(parent, entry) {
        var from = joinPath(parent, entry.name);
        promptName('Rename ' + entry.name, entry.name).then(function (name) {
          if (!name || name === entry.name) return;
          var to = joinPath(parent, name);
          $.ajax({
            url: baseURL() + '/rename',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ from: from, to: to })
          }).done(function () {
            refresh();
          }).fail(function (xhr) {
            DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to rename.');
          });
        });
      }

      function remove(parent, entry) {
        var path = joinPath(parent, entry.name);
        DS.confirm('Delete ' + path + (entry.type === 'dir' ? ' and everything in it' : '') + '?').then(function (ok) {
          if (!ok) return;
          $.ajax({ url: baseURL() + '?path=' + encodeURIComponent(path), method: 'DELETE' }).done(function () {
            invalidate(path);
            render();
          }).fail(function (xhr) {
            DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to delete.');
          });
        });
      }

      function openMenuFor(parent, entry, x, y) {
        var items = [];
        if (!entry) {
          items.push(
            menuItem('New file…', 'fa-file', '', function () { newFile(parent); }),
            menuItem('New folder…', 'fa-folder-plus', '', function () { newFolder(parent); }),
            menuItem('Refresh', 'fa-arrows-rotate', '', refresh)
          );
        } else if (entry.type === 'dir') {
          var path = joinPath(parent, entry.name);
          items.push(
            menuItem('New file…', 'fa-file-circle-plus', '', function () { newFile(path); }),
            menuItem('New folder…', 'fa-folder-plus', '', function () { newFolder(path); }),
            menuItem('Rename…', 'fa-i-cursor', '', function () { rename(parent, entry); }),
            menuItem('Delete…', 'fa-trash', 'text-danger', function () { remove(parent, entry); }),
            menuItem('Refresh', 'fa-arrows-rotate', '', function () { invalidate(path); render(); })
          );
        } else {
          items.push(
            menuItem('Open', 'fa-file-lines', '', function () { onOpenFile(joinPath(parent, entry.name)); }),
            menuItem('Rename…', 'fa-i-cursor', '', function () { rename(parent, entry); }),
            menuItem('Delete…', 'fa-trash', 'text-danger', function () { remove(parent, entry); })
          );
        }
        openMenu(x, y, items);
      }

      // Background menu on the container itself (a row's own handler stops
      // propagation, so reaching here means bare space was right-clicked).
      $container.on('contextmenu', function (e) {
        e.preventDefault();
        openMenuFor('.', null, e.pageX, e.pageY);
      });

      render();

      return { refresh: refresh, invalidate: invalidate };
    }
  };
})();