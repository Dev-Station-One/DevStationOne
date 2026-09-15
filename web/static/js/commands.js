// DevStationOne Quick Commands (ADR-045): per-project CLI commands rendered
// into the project workspace's "Commands" app tab. Each run opens a real
// PTY session on the project's runtime (the run endpoint returns a
// terminal-session object; the host page's onSession callback turns it into
// a terminal tab), with per-run history and the captured output tail.
//
// ponytail: one self-contained module injected into the panel, same shape
// as web/static/js/backups.js — no framework, no new modal component.
(function () {
  'use strict';

  var state = null; // { projectID, onSession }

  function esc(s) {
    var d = document.createElement('div');
    d.textContent = s == null ? '' : String(s);
    return d.innerHTML;
  }

  function timeAgo(iso) {
    var diff = (Date.now() - new Date(iso).getTime()) / 1000;
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
  }

  function duration(since, until) {
    var ms = new Date(until).getTime() - new Date(since).getTime();
    if (ms < 0) ms = 0;
    if (ms < 1000) return '<1s';
    var s = Math.round(ms / 1000);
    if (s < 60) return s + 's';
    return Math.floor(s / 60) + 'm ' + (s % 60) + 's';
  }

  function apiBase() {
    return '/api/v1/projects/' + state.projectID;
  }

  function showError(xhr, fallback) {
    var msg = (xhr.responseJSON && xhr.responseJSON.error) || fallback;
    $('#commands-error').text(msg).prop('hidden', false);
  }

  // --- list ---

  function load() {
    $('#commands-loading').prop('hidden', false);
    $('#commands-error').prop('hidden', true);
    $('#commands-body').prop('hidden', true);
    $('#commands-empty').prop('hidden', true);
    $.get(apiBase() + '/commands').done(function (list) {
      $('#commands-loading').prop('hidden', true);
      renderRows(list);
    }).fail(function (xhr) {
      $('#commands-loading').prop('hidden', true);
      showError(xhr, 'Failed to load commands.');
    });
  }

  function renderRows(list) {
    var $list = $('#commands-rows').empty();
    $('#commands-empty').prop('hidden', list.length > 0);
    $('#commands-body').prop('hidden', list.length === 0);
    list.forEach(function (c) {
      var $row = $(
        '<div class="list-group-item py-2">'
        + '<div class="d-flex align-items-center gap-2 flex-wrap">'
        + '<div class="flex-grow-1 min-width-0">'
        + '<div class="fw-semibold small">' + esc(c.name) + '</div>'
        + '<code class="text-secondary small ds-cmd-text d-block text-truncate" title="' + esc(c.command) + '">' + esc(c.command) + '</code>'
        + '</div>'
        + '<button type="button" class="btn btn-primary btn-sm ds-cmd-run" data-id="' + c.id + '">'
        + '<i class="fa-solid fa-play me-1" aria-hidden="true"></i>Run</button>'
        + '<button type="button" class="icon-btn ds-cmd-edit" data-id="' + c.id + '" aria-label="Edit ' + esc(c.name) + '" title="Edit"><i class="fa-solid fa-pencil" aria-hidden="true"></i></button>'
        + '<button type="button" class="icon-btn text-danger ds-cmd-delete" data-id="' + c.id + '" aria-label="Delete ' + esc(c.name) + '" title="Delete"><i class="fa-solid fa-trash" aria-hidden="true"></i></button>'
        + '</div>'
        + '<details class="ds-cmd-history" data-id="' + c.id + '">'
        + '<summary class="text-secondary small">History</summary>'
        + '<div class="ds-cmd-runs mt-1"></div>'
        + '</details>'
        + '</div>'
      );
      $row.data('cmd', c);
      // The native `toggle` event does not bubble, so this one is bound
      // directly per row rather than delegated to #commands-rows.
      $row.find('.ds-cmd-history').on('toggle', function (e) {
        if (e.target.open && !e.target.dataset.loaded) {
          e.target.dataset.loaded = '1';
          loadHistory($(e.target));
        }
      });
      $('#commands-rows').append($row);
    });
  }

  // --- run ---

  function run(c, $btn) {
    $btn.prop('disabled', true);
    // Fire-and-collect (ADR-045): the run's session is created server-side
    // but the browser never attaches — the 2s poll below refetches the row
    // (and then the list) once the session.exit finalize lands.
    $.ajax({ url: apiBase() + '/commands/' + c.id + '/run', method: 'POST' }).done(function () {
      $btn.prop('disabled', false);
      pollRun(c.id);
    }).fail(function (xhr) {
      $btn.prop('disabled', false);
      DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to run command.');
    });
  }

  var pollTimer = null;

  function pollRun(commandID) {
    if (pollTimer) return; // one watcher at a time; Refresh covers the rest
    pollTimer = setInterval(function () {
      $.get(apiBase() + '/command-runs?command_id=' + encodeURIComponent(commandID) + '&limit=1').done(function (res) {
        var run = res.runs[0];
        if (!run || run.finished_at) {
          clearInterval(pollTimer);
          pollTimer = null;
          load();
        }
      }).fail(function () {
        clearInterval(pollTimer);
        pollTimer = null;
      });
    }, 2000);
  }

  function onSession(info) {
    if (state.onSession) state.onSession(info);
  }

  // --- history ---

  function loadHistory($details) {
    var c = $details.closest('.list-group-item').data('cmd');
    var $host = $details.find('.ds-cmd-runs');
    $.get(apiBase() + '/command-runs?command_id=' + encodeURIComponent(c.id) + '&limit=10').done(function (res) {
      $host.empty();
      if (!res.runs.length) {
        $host.append('<div class="text-secondary small py-1">No runs yet.</div>');
        return;
      }
      res.runs.forEach(function (run) {
        var ok = run.exit_code === 0;
        var $item = $('<div class="d-flex align-items-center gap-2 py-1 border-top small">');
        var badge = run.exit_code == null ? (run.finished_at ? '<span class="badge bg-secondary-lt">no exit</span>' : '<span class="badge bg-warning-lt">running</span>')
          : '<span class="badge ' + (run.exit_code === 0 ? 'bg-success-lt' : 'bg-red-lt') + '">exit ' + esc(run.exit_code) + '</span>';
        $item.append(badge);
        $item.append('<span class="text-secondary" style="min-width: 5.5em;">' + esc(timeAgo(run.started_at)) + '</span>');
        $item.append('<span class="text-secondary">' + esc(run.run_by_name || '') + '</span>');
        if (run.finished_at) {
          $item.append('<span class="text-secondary" style="min-width: 3em;">' + esc(duration(run.started_at, run.finished_at)) + '</span>');
          $item.append('<button type="button" class="btn btn-sm ds-cmd-out" data-id="' + run.id + '">Output</button>');
        }
        if (run.error) {
          $item.append('<span class="text-danger small text-truncate" title="' + esc(run.error) + '">' + esc(run.error) + '</span>');
        }
        $host.append($item);
      });
    }).fail(function (xhr) {
      $host.text((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to load history.');
    });
  }

  function showOutput(runID) {
    $.get(apiBase() + '/command-runs/' + encodeURIComponent(runID)).done(function (run) {
      var tail = run.output_tail || (run.error ? 'Error: ' + run.error : '(no output captured)');
      var CLAMP = 1024 * 1024;
      var body = tail.length > CLAMP
        ? '[output truncated to the last 1 MiB for display — full output is stored]\n\n' + tail.slice(-CLAMP)
        : tail;
      $('#command-output-pre').text(body);
      DSCommands.modal('command-output-modal').show();
    });
  }

  // --- add / edit ---

  function openEditor(c) {
    $('#command-form-error').prop('hidden', true);
    $('#command-form-title').text(c ? 'Edit command' : 'New command');
    $('#command-name-input').val(c ? c.name : '');
    $('#command-text-input').val(c ? c.command : '');
    $('#command-form-modal').data('editing', c ? c.id : '');
    DSCommands.modal('command-form-modal').show();
  }

  function save() {
    var editID = $('#command-form-modal').data('editing');
    var body = {
      name: $('#command-name-input').val().trim(),
      command: $('#command-text-input').val().trim(),
      sort_order: editID ? commandsSortOrder(editID) : nextSortOrder()
    };
    if (!body.name || !body.command) {
      $('#command-form-error').text('Name and command are required.').prop('hidden', false);
      return;
    }
    var url = body.name && editID ? apiBase() + '/commands/' + editID : apiBase() + '/commands';
    $.ajax({ url: url, method: editID ? 'PATCH' : 'POST', contentType: 'application/json', data: JSON.stringify(body) })
      .done(function () {
        DSCommands.modal('command-form-modal').hide();
        load();
      })
      .fail(function (xhr) {
        $('#command-form-error').text((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to save.').prop('hidden', false);
      });
  }

  // commands is the last loaded list; sort_order helpers read it.
  var commands = [];

  function nextSortOrder() {
    var max = -1;
    commands.forEach(function (c) { if (c.sort_order > max) max = c.sort_order; });
    return max + 1;
  }

  function commandsSortOrder(id) {
    for (var i = 0; i < commands.length; i++) {
      if (commands[i].id === id) return commands[i].sort_order;
    }
    return 0;
  }

  function remove(c) {
    DS.confirm('Delete command "' + c.name + '"? Its run history is kept.').done(function (ok) {
      if (!ok) return;
      $.ajax({ url: apiBase() + '/commands/' + c.id, method: 'DELETE' }).done(load);
    });
  }

  // --- import (checkbox list over detected candidates; nothing saves
  // until "Import selected") ---

  function openImport() {
    $('#commands-import-body').html('<div class="text-secondary py-2">Scanning Makefile, package.json and scripts/…</div>');
    DSCommands.modal('commands-import-modal').show();
    $.ajax({ url: apiBase() + '/commands/import', method: 'POST' }).done(function (res) {
      $('#commands-import-body').empty();
      var bySource = { makefile: 'Makefile targets', npm: 'package.json scripts', scripts: 'scripts/' };
      Object.keys(bySource).forEach(function (source) {
        var group = res.candidates.filter(function (c) { return c.source === source; });
        if (!group.length) return;
        $('#commands-import-body').append('<div class="fw-bold small text-secondary mt-2 mb-1">' + esc(bySource[source]) + '</div>');
        group.forEach(function (c) {
          var id = 'imp-' + Math.random().toString(36).slice(2);
          $('#commands-import-body').append(
            '<label class="form-check ds-import-row">'
            + '<input type="checkbox" class="form-check-input ds-import-check" checked'
            + ' data-name="' + esc(c.name) + '" data-command="' + esc(c.command) + '">'
            + '<span class="form-check-label small"><strong>' + esc(c.name) + '</strong>'
            + ' <code class="text-secondary ms-1">' + esc(c.command) + '</code></span>'
            + '</label>'
          );
        });
      });
      if (!res.candidates.length) {
        $('#commands-import-body').append('<div class="text-secondary py-2">Nothing detected — no Makefile targets, package.json scripts, or executable scripts/ files.</div>');
      }
      $('#commands-import-accept').prop('disabled', !res.candidates.length);
    }).fail(function (xhr) {
      $('#commands-import-body').html('<div class="alert alert-danger mb-0">' + esc((xhr.responseJSON && xhr.responseJSON.error) || 'Scan failed — is the runtime online?') + '</div>');
      $('#commands-import-accept').prop('disabled', true);
    });
  }

  function importSelected() {
    var picked = $('#commands-import-body .ds-import-check:checked');
    var base = commands.length;
    var complete = 0;
    var finish = function () {
      complete++;
      if (complete >= picked.length) {
        DSCommands.modal('commands-import-modal').hide();
        load();
      }
    };
    picked.each(function (i) {
      var $cb = $(this);
      $.ajax({
        url: apiBase() + '/commands', method: 'POST', contentType: 'application/json',
        data: JSON.stringify({ name: $cb.data('name'), command: $cb.data('command'), sort_order: base + i })
      }).done(finish).fail(function (xhr) {
        DS.error((xhr.responseJSON && xhr.responseJSON.error) || 'Failed to import.');
        finish();
      });
    });
    if (!picked.length) {
      DSCommands.modal('commands-import-modal').hide();
    }
  }

  // --- modals (injected once into <body>; driven by bootstrap.Modal's JS
  // API, never the Data API — ADR-034) ---

  function modal(id) {
    var el = document.getElementById(id);
    if (!el) injectModals();
    return bootstrap.Modal.getOrCreateInstance(document.getElementById(id));
  }

  function injectModals() {
    if (document.getElementById('command-form-modal')) return;
    var wrap = document.createElement('div');
    wrap.innerHTML =
      '<div class="modal fade" id="command-form-modal" tabindex="-1" aria-hidden="true">'
      + '<div class="modal-dialog modal-dialog-centered"><div class="modal-content">'
      + '<div class="modal-header"><h5 class="modal-title" id="command-form-title">New command</h5>'
      + '<button type="button" id="command-form-close" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>'
      + '<div class="modal-body">'
      + '<div id="command-form-error" class="alert alert-danger" hidden></div>'
      + '<label class="form-label" for="command-name-input">Name</label>'
      + '<input id="command-name-input" class="form-control mb-2" autocomplete="off">'
      + '<label class="form-label" for="command-text-input">Command</label>'
      + '<textarea id="command-text-input" class="form-control font-monospace" rows="3" spellcheck="false"></textarea>'
      + '</div>'
      + '<div class="modal-footer"><button type="button" id="command-form-cancel" class="btn btn-secondary">Cancel</button>'
      + '<button type="button" id="command-form-save" class="btn btn-primary">Save</button></div>'
      + '</div></div></div>'
      + '<div class="modal fade" id="commands-import-modal" tabindex="-1" aria-hidden="true">'
      + '<div class="modal-dialog modal-lg modal-dialog-centered modal-dialog-scrollable"><div class="modal-content">'
      + '<div class="modal-header"><h5 class="modal-title">Import commands</h5>'
      + '<button type="button" id="commands-import-close" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>'
      + '<div class="modal-body" id="commands-import-body"></div>'
      + '<div class="modal-footer"><button type="button" id="commands-import-cancel" class="btn btn-secondary">Cancel</button>'
      + '<button type="button" id="commands-import-accept" class="btn btn-primary">Import selected</button></div>'
      + '</div></div></div>'
      + '<div class="modal fade" id="command-output-modal" tabindex="-1" aria-hidden="true">'
      + '<div class="modal-dialog modal-lg modal-dialog-centered"><div class="modal-content">'
      + '<div class="modal-header"><h5 class="modal-title">Run output</h5>'
      + '<button type="button" id="command-output-close" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>'
      + '<div class="modal-body"><pre id="command-output-pre" class="mb-0 small bg-dark-subtle p-2" style="max-height: 60vh; overflow-y: auto; white-space: pre-wrap;"></pre></div>'
      + '</div></div></div>';
    // appendChild per modal root (wrap holds three siblings).
    while (wrap.firstElementChild) document.body.appendChild(wrap.firstElementChild);

    document.getElementById('command-form-close').addEventListener('click', function () { modal('command-form-modal').hide(); });
    document.getElementById('command-form-cancel').addEventListener('click', function () { modal('command-form-modal').hide(); });
    document.getElementById('command-form-save').addEventListener('click', save);
    document.getElementById('commands-import-close').addEventListener('click', function () { modal('commands-import-modal').hide(); });
    document.getElementById('commands-import-cancel').addEventListener('click', function () { modal('commands-import-modal').hide(); });
    document.getElementById('commands-import-accept').addEventListener('click', importSelected);
    document.getElementById('command-output-close').addEventListener('click', function () { modal('command-output-modal').hide(); });
  }

  // --- entry point (project.html's lazy-tab wiring) ---

  function render($panel, opts) {
    state = { projectID: opts.projectID };
    $panel.html(
      '<div class="d-flex justify-content-between align-items-center mb-3">'
      + '<div id="commands-summary" class="text-secondary">—</div>'
      + '<div class="d-flex gap-2">'
      + '<button type="button" id="commands-import-btn" class="btn btn-sm"><i class="fa-solid fa-file-import me-1" aria-hidden="true"></i>Import</button>'
      + '<button type="button" id="commands-add-btn" class="btn btn-primary btn-sm"><i class="fa-solid fa-plus me-1" aria-hidden="true"></i>New command</button>'
      + '<button type="button" id="commands-refresh-btn" class="icon-btn" aria-label="Refresh commands" title="Refresh"><i class="fa-solid fa-rotate" aria-hidden="true"></i></button>'
      + '</div></div>'
      + '<div id="commands-loading" class="text-secondary">Loading commands…</div>'
      + '<div id="commands-error" class="alert alert-danger" hidden></div>'
      + '<div id="commands-empty" class="card" hidden>'
      + '<div class="card-body text-secondary"><i class="fa-solid fa-bolt me-1" aria-hidden="true"></i>No commands yet. Add one by hand or import from a Makefile, package.json, or the scripts/ folder.</div>'
      + '</div>'
      + '<div id="commands-body" hidden><div id="commands-rows" class="list-group list-group-flush"></div></div>'
    );
    injectModals();

    $('#commands-add-btn').on('click', function () { openEditor(null); });
    $('#commands-import-btn').on('click', openImport);
    $('#commands-refresh-btn').on('click', load);
    $('#command-form-save').on('click', save);

    $('#commands-rows').on('click', '.ds-cmd-run', function () {
      var id = $(this).data('id');
      var row = $(this).closest('.list-group-item').data('cmd');
      run(row, $(this));
    }).on('click', '.ds-cmd-edit', function () {
      openEditor($(this).closest('.list-group-item').data('cmd'));
    }).on('click', '.ds-cmd-delete', function () {
      remove($(this).closest('.list-group-item').data('cmd'));
    }).on('click', '.ds-cmd-out', function () {
      showOutput($(this).data('id'));
    });

    load();
  }

  window.DSCommands = { render: render, modal: modal };
})();
