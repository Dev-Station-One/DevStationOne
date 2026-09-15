// Shared backup/restore panel renderer (docs/REQUIREMENTS_BACKUPS_SERVICES.md,
// docs/DESIGN_BACKUPS_SERVICES.md). Used for both a database connection's
// backups (kind: 'db') and a project's backups (kind: 'project') — the API
// shape (list/create/delete/download/restore/schedule) is identical for
// both, scoped by apiBase.
(function () {
  'use strict';

  function stateBadge(b) {
    if (b.state === 'done') return '<span class="badge bg-success">done</span>';
    if (b.state === 'failed') return '<span class="badge bg-danger" title="' + $('<div>').text(b.error || '').html() + '">failed</span>';
    return '<span class="badge bg-info">running</span>';
  }

  function triggerBadge(b) {
    var cls = b.trigger === 'manual' ? 'bg-azure-lt' : (b.trigger === 'schedule' ? 'bg-purple-lt' : 'bg-yellow-lt');
    return '<span class="badge ' + cls + '">' + b.trigger + '</span>';
  }

  function fmtSize(n) {
    if (!n) return '—';
    var units = ['B', 'KB', 'MB', 'GB'];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return n.toFixed(i ? 1 : 0) + ' ' + units[i];
  }

  // render mounts a full backups panel (list + "Back up now" + schedule)
  // into $container. opts: { apiBase, kind: 'db'|'project' }.
  function render($container, opts) {
    var pollTimer = null;

    $container.empty().append(
      '<div class="d-flex justify-content-between align-items-center mb-2">' +
        '<strong>Backups</strong>' +
        '<button type="button" class="btn btn-sm btn-primary" data-action="backup-now"><i class="fa-solid fa-plus me-1" aria-hidden="true"></i>Back up now</button>' +
      '</div>' +
      '<div class="ds-backups-list mb-3"></div>' +
      '<div class="ds-backups-schedule"></div>'
    );
    var $list = $container.find('.ds-backups-list');
    var $schedule = $container.find('.ds-backups-schedule');

    function refreshList() {
      $list.empty().append($('<div class="text-secondary small">').text('Loading…'));
      $.get(opts.apiBase + '/backups').done(function (list) {
        $list.empty();
        var anyRunning = false;
        if (!list.length) {
          $list.append($('<div class="text-secondary small">').text('No backups yet.'));
        }
        list.forEach(function (b) {
          if (b.state === 'running') anyRunning = true;
          var $row = $('<div class="d-flex align-items-center gap-2 py-1 border-bottom small flex-wrap">').appendTo($list);
          $row.append(
            $('<span class="text-secondary" style="min-width:9rem">').text(new Date(b.created_at).toLocaleString()),
            $(stateBadge(b)), $(triggerBadge(b)),
            $('<span class="text-secondary">').text(fmtSize(b.size_bytes))
          );
          var $actions = $('<span class="ms-auto d-flex gap-1">').appendTo($row);
          if (b.state === 'done') {
            $('<a class="icon-btn" aria-label="Download" title="Download">').attr('href', opts.apiBase + '/backups/' + b.id + '/download')
              .append('<i class="fa-solid fa-download" aria-hidden="true"></i>').appendTo($actions);
            if (opts.kind === 'db') {
              $('<button type="button" class="icon-btn" aria-label="Restore" title="Restore">')
                .append('<i class="fa-solid fa-clock-rotate-left" aria-hidden="true"></i>')
                .on('click', function () { restoreDB(b); }).appendTo($actions);
            } else {
              $('<button type="button" class="icon-btn" aria-label="Restore as new project" title="Restore as new project">')
                .append('<i class="fa-solid fa-clone" aria-hidden="true"></i>')
                .on('click', function () { restoreProject(b, 'new'); }).appendTo($actions);
              $('<button type="button" class="icon-btn text-danger" aria-label="Restore over this project" title="Restore over this project">')
                .append('<i class="fa-solid fa-clock-rotate-left" aria-hidden="true"></i>')
                .on('click', function () { restoreProject(b, 'overwrite'); }).appendTo($actions);
            }
          }
          $('<button type="button" class="icon-btn text-danger" aria-label="Delete" title="Delete">')
            .append('<i class="fa-solid fa-trash" aria-hidden="true"></i>')
            .on('click', function () { deleteBackup(b); }).appendTo($actions);
        });
        if (anyRunning) {
          clearTimeout(pollTimer);
          pollTimer = setTimeout(refreshList, 2000);
        }
      }).fail(function (xhr) {
        $list.empty().append($('<div class="text-danger small">').text((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to load backups.'));
      });
    }

    function deleteBackup(b) {
      DS.confirm('Delete this backup? This cannot be undone.').then(function (ok) {
        if (!ok) return;
        $.ajax({ url: opts.apiBase + '/backups/' + b.id, method: 'DELETE' })
          .done(refreshList)
          .fail(function (xhr) { DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to delete backup.'); });
      });
    }

    function restoreDB(b) {
      DS.prompt({ title: 'Restore into database (existing or new name):' }).then(function (target) {
        if (!target) return;
        DS.confirm('Restore into "' + target + '"? If it already exists, its data will be replaced.').then(function (ok) {
          if (!ok) return;
          $.ajax({
            url: opts.apiBase + '/backups/' + b.id + '/restore', method: 'POST', contentType: 'application/json',
            data: JSON.stringify({ target_database: target, confirm: true })
          }).done(function () {
            DS.error('Restore complete.');
          }).fail(function (xhr) { DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Restore failed.'); });
        });
      });
    }

    function restoreProject(b, mode) {
      if (mode === 'new') {
        DS.confirm('Restore this backup as a new project?').then(function (ok) {
          if (!ok) return;
          $.ajax({
            url: opts.apiBase + '/backups/' + b.id + '/restore', method: 'POST', contentType: 'application/json',
            data: JSON.stringify({ mode: 'new' })
          }).done(function (r) {
            DS.error('Restored as new project "' + r.name + '".').then(function () { window.location.href = '/project.html?id=' + r.project_id; });
          }).fail(function (xhr) { DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Restore failed.'); });
        });
        return;
      }
      DS.confirmTypedName(
        'Type the project name to confirm overwriting it with this backup. Open sessions will be closed and a safety snapshot is taken first.',
        opts.projectName
      ).then(function (name) {
        if (!name) return;
        $.ajax({
          url: opts.apiBase + '/backups/' + b.id + '/restore', method: 'POST', contentType: 'application/json',
          data: JSON.stringify({ mode: 'overwrite', confirm_name: name })
        }).done(function () {
          DS.error('Restore complete.');
        }).fail(function (xhr) { DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Restore failed.'); });
      });
    }

    $container.find('[data-action="backup-now"]').on('click', function () {
      $(this).prop('disabled', true);
      $.ajax({ url: opts.apiBase + '/backups', method: 'POST' })
        .done(refreshList)
        .fail(function (xhr) { DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to start backup.'); })
        .always(function () { $(this).prop('disabled', false); }.bind(this));
    });

    function refreshSchedule() {
      $.get(opts.apiBase + '/backups/schedule')
        .done(function (s) { renderSchedule(s); })
        .fail(function (xhr) { renderSchedule(xhr.status === 404 ? null : null); });
    }

    function renderSchedule(s) {
      $schedule.empty();
      var $row = $('<div class="d-flex flex-wrap align-items-end gap-2">').appendTo($schedule);
      var $cadence = $('<select class="form-select form-select-sm" style="width:110px;">');
      ['hourly', 'daily', 'weekly'].forEach(function (c) { $cadence.append($('<option>').val(c).text(c)); });
      var $retention = $('<input type="number" min="1" max="100" class="form-control form-control-sm" style="width:80px;" value="10">');
      if (s) { $cadence.val(s.cadence); $retention.val(s.retention); }
      $row.append(
        $('<div>').append($('<label class="form-label small mb-1">').text('Schedule'), $cadence),
        $('<div>').append($('<label class="form-label small mb-1">').text('Keep'), $retention),
        $('<button type="button" class="btn btn-sm btn-outline-secondary">').text(s ? 'Update' : 'Enable').on('click', function () {
          $.ajax({
            url: opts.apiBase + '/backups/schedule', method: 'PUT', contentType: 'application/json',
            data: JSON.stringify({ cadence: $cadence.val(), retention: parseInt($retention.val(), 10) || 10 })
          }).done(refreshSchedule).fail(function (xhr) { DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to save schedule.'); });
        })
      );
      if (s) {
        $row.append($('<button type="button" class="btn btn-sm btn-outline-danger">').text('Remove').on('click', function () {
          $.ajax({ url: opts.apiBase + '/backups/schedule', method: 'DELETE' }).done(refreshSchedule);
        }));
        var status = s.last_run_at
          ? ('Last run ' + new Date(s.last_run_at).toLocaleString() + ' — ' + (s.last_result || '—') + (s.last_error ? ' (' + s.last_error + ')' : ''))
          : 'Not run yet.';
        $schedule.append($('<div class="text-secondary small mt-1">').text(status + ' · Next: ' + new Date(s.next_run_at).toLocaleString()));
      } else {
        $schedule.append($('<div class="text-secondary small mt-1">').text('No schedule — backups are manual only.'));
      }
    }

    refreshList();
    refreshSchedule();
  }

  window.DSBackups = { render: render };
})();