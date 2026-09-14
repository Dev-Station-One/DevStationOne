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

    // Directory picker for Project setup: browses runtimeId's home
    // directory via GET/POST .../runtimes/{id}/browse[/mkdir] (there is no
    // Project yet at this point, so the runtime's home dir is the browse
    // root — see docs/PROTOCOL.md). Resolves the chosen path relative to
    // that home dir ("." for the home dir itself, "src/app" for a
    // subfolder), or null on cancel. The caller joins it onto the
    // runtime's capabilities.home_dir to get an absolute path.
    browseDirectory: function (runtimeId) {
      var path = '.';

      function list() {
        return $.get('/api/v1/runtimes/' + runtimeId + '/browse', { path: path });
      }

      function mkdir(name) {
        var newPath = path === '.' ? name : path + '/' + name;
        return $.ajax({
          url: '/api/v1/runtimes/' + runtimeId + '/browse/mkdir',
          method: 'POST',
          contentType: 'application/json',
          data: JSON.stringify({ path: newPath })
        }).done(function () { path = newPath; });
      }

      function render() {
        var $c = $(Swal.getHtmlContainer()).empty();

        var $crumbs = $('<div class="d-flex flex-wrap align-items-center gap-1 mb-2"></div>');
        $crumbs.append(
          $('<button type="button" class="btn btn-sm btn-outline-secondary">')
            .html('<i class="fa-solid fa-house" aria-hidden="true"></i> Home')
            .on('click', function () { path = '.'; render(); })
        );
        if (path !== '.') {
          var accum = [];
          path.split('/').forEach(function (seg) {
            accum.push(seg);
            var p = accum.join('/');
            $crumbs.append($('<span class="px-1 text-secondary">/</span>'));
            $crumbs.append(
              $('<button type="button" class="btn btn-sm btn-link p-0">').text(seg)
                .on('click', function () { path = p; render(); })
            );
          });
        }
        $c.append($crumbs);

        var $newFolder = $(
          '<div class="input-group input-group-sm mb-2">' +
            '<input type="text" class="form-control" placeholder="New folder name">' +
            '<button class="btn btn-outline-secondary" type="button">Create</button>' +
          '</div>'
        );
        $newFolder.find('button').on('click', function () {
          var name = $newFolder.find('input').val().trim();
          if (!name) return;
          mkdir(name).done(render).fail(function (xhr) {
            DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to create folder.');
          });
        });
        $c.append($newFolder);

        var $list = $('<div class="list-group" style="max-height:260px;overflow-y:auto"></div>').text('Loading…');
        $c.append($list);

        list().done(function (entries) {
          $list.empty();
          var dirs = entries.filter(function (e) { return e.type === 'dir'; });
          if (!dirs.length) {
            $list.append($('<div class="text-secondary p-2">').text('No subfolders here.'));
          }
          dirs.forEach(function (e) {
            var p = path === '.' ? e.name : path + '/' + e.name;
            $('<button type="button" class="list-group-item list-group-item-action">')
              .html('<i class="fa-solid fa-folder me-1" aria-hidden="true"></i>')
              .append(document.createTextNode(e.name))
              .on('click', function () { path = p; render(); })
              .appendTo($list);
          });
        }).fail(function (xhr) {
          $list.empty().append(
            $('<div class="text-danger p-2">').text((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to list directory.')
          );
        });
      }

      return Swal.fire(base({
        title: 'Choose a working directory',
        html: '<div></div>',
        showCancelButton: true,
        confirmButtonText: 'Select this folder',
        didOpen: render
      })).then(function (r) { return r.isConfirmed ? path : null; });
    }
  };
})();