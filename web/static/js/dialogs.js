// DevStation shared dialogs (vendored SweetAlert2): destructive confirms,
// text prompts, and blocking error modals. Styling follows
// docs/BOOTSTRAP_COLORS.md; theme follows the page's data-bs-theme.
(function () {
  'use strict';

  function base(extra) {
    var dark = document.documentElement.getAttribute('data-bs-theme') !== 'light';
    return Object.assign({
      theme: dark ? 'dark' : 'light',
      confirmButtonColor: 'var(--ds-primary)',
      confirmButtonText: 'OK'
    }, extra);
  }

  window.DS = {
    // True only on Confirm; Esc/Cancel/backdrop resolve false.
    confirm: function (text) {
      return Swal.fire(base({
        title: text,
        icon: 'warning',
        confirmButtonColor: 'var(--ds-danger)',
        confirmButtonText: 'Confirm',
        showCancelButton: true
      })).then(function (r) { return !!r.isConfirmed; });
    },

    // Resolves the submitted value, or null on cancel. Empty submits get a
    // validation message; native prompt returned '' and callers treated it
    // as cancel — same net effect.
    prompt: function (o) {
      return Swal.fire(base({
        title: o.title,
        input: 'text',
        inputValue: o.value || '',
        showCancelButton: true,
        preConfirm: function (v) {
          if (!v) {
            Swal.showValidationMessage('Required.');
            return false;
          }
          return v;
        }
      })).then(function (r) { return r.isConfirmed ? r.value : null; });
    },

    // Blocking error modal (replaces window.alert).
    error: function (text) {
      return Swal.fire(base({ title: text, icon: 'error' }));
    },

    // New-terminal-session prompt: name plus render mode (Slice 17).
    // Resolves {name, render_mode} or null on cancel.
    newSession: function (defaultName) {
      var html =
        '<div class="mb-3 text-start">' +
          '<label class="form-label">Name</label>' +
          '<input type="text" id="ds-new-session-name" class="form-control" value="' + (defaultName || 'bash') + '">' +
        '</div>' +
        '<div class="text-start">' +
          '<label class="form-label mb-1">Render mode</label>' +
          '<div class="form-check">' +
            '<input class="form-check-input" type="radio" name="ds-new-session-mode" id="ds-new-session-mode-xterm" value="xterm" checked>' +
            '<label class="form-check-label" for="ds-new-session-mode-xterm">Terminal (xterm)</label>' +
          '</div>' +
          '<div class="form-check">' +
            '<input class="form-check-input" type="radio" name="ds-new-session-mode" id="ds-new-session-mode-cards" value="cards">' +
            '<label class="form-check-label" for="ds-new-session-mode-cards">Cards <span class="text-secondary">(structured prompts/diffs)</span></label>' +
          '</div>' +
        '</div>';
      return Swal.fire(base({
        title: 'New terminal session',
        html: html,
        showCancelButton: true,
        didOpen: function () {
          $(Swal.getHtmlContainer()).find('#ds-new-session-name').trigger('focus').trigger('select');
        },
        preConfirm: function () {
          var $c = $(Swal.getHtmlContainer());
          var name = $c.find('#ds-new-session-name').val().trim();
          if (!name) {
            Swal.showValidationMessage('Required.');
            return false;
          }
          return { name: name, render_mode: $c.find('input[name="ds-new-session-mode"]:checked').val() };
        }
      })).then(function (r) { return r.isConfirmed ? r.value : null; });
    },

    // Directory picker for Project setup: an expandable, searchable tree
    // over runtimeId's home directory via GET/POST .../runtimes/{id}/browse*
    // (there is no Project yet at this point, so the runtime's home dir is
    // the browse root — see docs/PROTOCOL.md). Each folder's children are
    // fetched lazily on first expand and cached for the life of the dialog.
    // Resolves the selected path relative to that home dir ("." for the
    // home dir itself, "src/app" for a subfolder), or null on cancel. The
    // caller joins it onto the runtime's capabilities.home_dir to get an
    // absolute path.
    browseDirectory: function (runtimeId) {
      var cache = {};    // path -> { loaded, expanded, dirs: [{name}, ...] }
      var selected = '.';
      var showHidden = false;

      // ensureLoaded fetches and sorts (case-insensitive) a directory's
      // subfolders the first time it's needed, then caches them.
      function ensureLoaded(path) {
        var node = cache[path];
        if (node && node.loaded) return $.Deferred().resolve(node).promise();
        return $.get('/api/v1/runtimes/' + runtimeId + '/browse', { path: path }).then(function (entries) {
          node = cache[path] || (cache[path] = { loaded: false, expanded: true, dirs: [] });
          node.loaded = true;
          node.dirs = entries
            .filter(function (e) { return e.type === 'dir'; })
            .sort(function (a, b) { return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }); });
          return node;
        });
      }

      // expandTo marks every ancestor of fullPath (excluding fullPath
      // itself) as expanded, so the lazy-load chain in renderChildren walks
      // down and reveals it — used when a search result is picked.
      function expandTo(fullPath) {
        if (fullPath === '.') return;
        var parts = fullPath.split('/');
        var acc = '';
        for (var i = 0; i < parts.length - 1; i++) {
          acc = acc ? acc + '/' + parts[i] : parts[i];
          if (cache[acc]) cache[acc].expanded = true;
          else cache[acc] = { loaded: false, expanded: true, dirs: [] };
        }
      }

      function renderChildren($container, path, depth) {
        var node = cache[path];
        if (!node || !node.loaded) {
          if (!node) node = cache[path] = { loaded: false, expanded: true, dirs: [] };
          ensureLoaded(path).done(render);
          $container.append(
            $('<div class="text-secondary small">').css('padding-left', (depth * 1.25 + 1.5) + 'rem').text('Loading…')
          );
          return;
        }
        var dirs = node.dirs.filter(function (d) { return showHidden || d.name.charAt(0) !== '.'; });
        if (depth === 0 && !dirs.length) {
          $container.append($('<div class="text-secondary p-2">').text('No subfolders here.'));
        }
        dirs.forEach(function (d) {
          var childPath = path === '.' ? d.name : path + '/' + d.name;
          var childNode = cache[childPath];
          var expanded = !!(childNode && childNode.expanded);
          var $row = $('<div class="d-flex align-items-center gap-1 py-1 rounded">')
            .css({ 'padding-left': (depth * 1.25) + 'rem', cursor: 'pointer' });
          if (childPath === selected) $row.addClass('bg-body-secondary');
          $('<span class="text-secondary flex-shrink-0 d-inline-flex justify-content-center" style="width:1.1rem">')
            .html('<i class="fa-solid fa-chevron-' + (expanded ? 'down' : 'right') + '" aria-hidden="true"></i>')
            .on('click', function (e) {
              e.stopPropagation();
              if (cache[childPath]) cache[childPath].expanded = !cache[childPath].expanded;
              else cache[childPath] = { loaded: false, expanded: true, dirs: [] };
              render();
            })
            .appendTo($row);
          $('<span class="text-truncate">')
            .append('<i class="fa-solid fa-folder me-1 text-secondary" aria-hidden="true"></i>')
            .append(document.createTextNode(d.name))
            .appendTo($row);
          $row.on('click', function () { selected = childPath; render(); });
          $container.append($row);
          if (expanded) renderChildren($container, childPath, depth + 1);
        });
      }

      function runSearch($input, $results) {
        var q = $input.val().trim();
        if (!q) { $results.hide().empty(); return; }
        $results.show().empty().append($('<div class="text-secondary p-2">').text('Searching…'));
        $.get('/api/v1/runtimes/' + runtimeId + '/browse/search', { q: q, path: '.' }).done(function (matches) {
          $results.empty();
          if (!matches.length) {
            $results.append($('<div class="text-secondary p-2">').text('No matching directories.'));
            return;
          }
          matches.forEach(function (m) {
            $('<button type="button" class="list-group-item list-group-item-action py-1">')
              .append('<i class="fa-solid fa-folder me-1 text-secondary" aria-hidden="true"></i>')
              .append(document.createTextNode(m))
              .on('click', function () {
                expandTo(m);
                selected = m;
                $input.val('');
                $results.hide().empty();
                render();
              })
              .appendTo($results);
          });
        }).fail(function (xhr) {
          $results.empty().append(
            $('<div class="text-danger p-2">').text((xhr.responseJSON && xhr.responseJSON.error) || 'Search failed.')
          );
        });
      }

      function render() {
        var $host = $(Swal.getHtmlContainer());
        var treeScroll = $host.find('.ds-tree').scrollTop();
        var $c = $host.empty();

        var $search = $(
          '<div class="input-group input-group-sm mb-2">' +
            '<input type="text" class="form-control" placeholder="Search directories…">' +
            '<button class="btn btn-outline-secondary" type="button"><i class="fa-solid fa-magnifying-glass" aria-hidden="true"></i></button>' +
          '</div>'
        );
        var $searchInput = $search.find('input');
        var $results = $('<div class="list-group mb-2" style="max-height:160px;overflow-y:auto"></div>').hide();
        $search.find('button').on('click', function () { runSearch($searchInput, $results); });
        $searchInput.on('keydown', function (e) {
          if (e.key === 'Enter') { e.preventDefault(); runSearch($searchInput, $results); }
        });
        $c.append($search).append($results);

        var $toolbar = $('<div class="d-flex flex-wrap align-items-center justify-content-between gap-2 mb-2">');
        $toolbar.append(
          $('<button type="button" class="btn btn-sm btn-outline-secondary">')
            .html('<i class="fa-solid fa-house" aria-hidden="true"></i> Home')
            .toggleClass('active', selected === '.')
            .on('click', function () { selected = '.'; render(); })
        );
        var $hidden = $(
          '<div class="form-check form-check-sm mb-0">' +
            '<input class="form-check-input" type="checkbox" id="ds-tree-show-hidden">' +
            '<label class="form-check-label" for="ds-tree-show-hidden">Show hidden</label>' +
          '</div>'
        );
        $hidden.find('input').prop('checked', showHidden).on('change', function () {
          showHidden = $(this).is(':checked');
          render();
        });
        $toolbar.append($hidden);
        $c.append($toolbar);

        $c.append($('<div class="small text-secondary mb-2">').append('Selected: ').append($('<code>').text(selected)));

        var $newFolder = $(
          '<div class="input-group input-group-sm mb-2">' +
            '<input type="text" class="form-control" placeholder="New folder name in selected directory">' +
            '<button class="btn btn-outline-secondary" type="button">Create</button>' +
          '</div>'
        );
        $newFolder.find('button').on('click', function () {
          var name = $newFolder.find('input').val().trim();
          if (!name) return;
          var parent = selected;
          var newPath = parent === '.' ? name : parent + '/' + name;
          $.ajax({
            url: '/api/v1/runtimes/' + runtimeId + '/browse/mkdir',
            method: 'POST',
            contentType: 'application/json',
            data: JSON.stringify({ path: newPath })
          }).done(function () {
            expandTo(newPath);
            cache[parent].loaded = false;
            selected = newPath;
            render();
          }).fail(function (xhr) {
            DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to create folder.');
          });
        });
        $c.append($newFolder);

        var $tree = $('<div class="ds-tree" style="max-height:280px;overflow-y:auto"></div>');
        $c.append($tree);
        renderChildren($tree, '.', 0);
        if (treeScroll) $tree.scrollTop(treeScroll);
      }

      return Swal.fire(base({
        title: 'Choose a working directory',
        html: '<div></div>',
        showCancelButton: true,
        confirmButtonText: 'Select this folder',
        didOpen: render
      })).then(function (r) { return r.isConfirmed ? selected : null; });
    }
  };
})();