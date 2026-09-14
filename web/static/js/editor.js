// DevStation Code tab editor pane: tabbed CodeMirror 6 editors with
// debounced whole-file autosave over the project's editor stream
// (docs/API.md "Editor (Code tab)", docs/PROTOCOL.md editor commands,
// docs/CODE_TAB_REQUIREMENTS.md). External changes arrive as whole-file
// content pushes; saves are last-write-wins with the pre-write content
// returned on a stale base fingerprint.
//
// API: DS.codePane.init({ projectID, switchToFiles }) — project.html calls
// it once on first Code-tab visit. switchToFiles is a deep-link affordance
// for oversize refusals.
(function () {
  'use strict';

  // The esbuild IIFE exposes the entry's namespace twice: window.CM6.CM6
  // (the named export) and window.CM6.default. Normalize to the API object.
  function CM6API() {
    var w = window.CM6;
    return (w && w.EditorState) ? w : (w && (w.CM6 || w.default)) || w;
  }

  function languageFor(path) {
    var n = path.toLowerCase();
    var i = n.lastIndexOf('.');
    var ext = i === -1 ? '' : n.slice(i + 1);
    var C = CM6API();
    switch (ext) {
      case 'js': case 'mjs': case 'jsx': return C.javascript({ jsx: true });
      case 'ts': case 'tsx': return C.javascript({ jsx: true, typescript: true });
      case 'html': case 'htm': return C.html();
      case 'css': return C.css();
      case 'json': return C.json();
      case 'yml': case 'yaml': return C.yaml();
      case 'md': case 'markdown': return C.markdown();
      case 'sql': return C.sql();
      case 'py': return C.python();
      case 'go': return C.go();
      default: return null;
    }
  }

  // Always-dark syntax palette (One-Dark-ish). The editor pane is dark
  // regardless of the Tabler theme (docs/CODE_TAB_REQUIREMENTS.md).
  var darkTheme = null;
  function theme() {
    if (darkTheme) return darkTheme;
    var C = CM6API();
    var highlight = C.HighlightStyle.define([
      { tag: 'keyword', color: '#c678dd' },
      { tag: ['name', 'propertyName'], color: '#e5c07b' },
      { tag: ['literal', 'constant'], color: '#d19a66' },
      { tag: 'string', color: '#98c379' },
      { tag: ['number', 'integer'], color: '#d19a66' },
      { tag: ['comment', 'blockComment'], color: '#7f848e', fontStyle: 'italic' },
      { tag: 'operator', color: '#56b6c2' },
      { tag: 'typeName', color: '#61afef' },
      { tag: 'function(variableName)', color: '#61afef' },
      { tag: 'definition(variable)', color: '#e06c75' },
      { tag: 'link', color: '#61afef', textDecoration: 'underline' },
      { tag: 'heading', color: '#e06c75', fontWeight: 'bold' },
      { tag: 'invalid', color: '#f00' }
    ]);
    darkTheme = [
      C.EditorView.theme({
        '&': { color: '#abb2bf', backgroundColor: '#1e2229', height: '100%' },
        '.cm-content': { caretColor: '#61afef' },
        '.cm-gutters': { backgroundColor: '#1e2229', color: '#5c6370', border: 'none' },
        '.cm-activeLine': { backgroundColor: '#2a2f38' },
        '.cm-activeLineGutter': { backgroundColor: '#2a2f38', color: '#c8ccd4' },
        '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: '#3e4451' },
        '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#61afef' },
        '.cm-matchingBracket': { backgroundColor: '#3e4451', outline: '1px solid #56b6c2' }
      }, { dark: true }),
      C.syntaxHighlighting(highlight)
    ];
    return darkTheme;
  }

  // Font/size from the account preferences (window.dsFontPref, set by
  // project.html's font preference block).
  var fontCompartment = null;
  function fontExt() {
    var C = CM6API();
    if (!fontCompartment) fontCompartment = new C.Compartment();
    return fontCompartment;
  }

  function fontSpec() {
    var C = CM6API();
    var prefs = window.dsFontPref || { editor: 'system', size: 14 };
    var stacks = {
      'fira-code': '"Fira Code", monospace',
      'cascadia-code': '"Cascadia Code", monospace',
      'source-code-pro': '"Source Code Pro", monospace'
    };
    return C.EditorView.theme({
      '.cm-content': { fontFamily: stacks[prefs.editor] || '"Fira Code", monospace', fontSize: prefs.size + 'px' },
      '.cm-gutters': { fontSize: prefs.size + 'px' }
    });
  }

  function refreshFonts() {
    if (fontCompartment) {
      tabs.forEach(function (t) {
        t.view.dispatch({ effects: fontCompartment.reconfigure(fontSpec()) });
      });
    }
  }

  var pane = null;
  window.DS = window.DS || {};
  window.DS.codePane = {
    init: function (opts) {
      if (pane) return pane;
      pane = build(opts);
      return pane;
    },
    openPath: function (path) {
      if (pane) pane.open(path);
    },
    applyPrefs: function () {
      if (fontCompartment) refreshFonts();
    }
  };

  var tabs = []; // set by build(); module-level so refreshFonts can reach it

  function build(opts) {
    var projectID = opts.projectID;
    var switchToFiles = opts.switchToFiles || function () {};
    var C = CM6API();

    var $tabs = $('#code-tabs');
    var $empty = $('#code-empty');
    var $stack = $('#code-editor-stack');
    var stateURL = '/api/v1/projects/' + projectID + '/editor/state';

    var activePath = null;
    var restoring = false;
    var hostSeq = 0;

    // --- editor stream ---

    var ws = null;
    var wsTries = 0;
    var wsTimer = null;

    function streamURL() {
      var proto = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
      return proto + window.location.host + '/api/v1/projects/' + projectID + '/editor/stream';
    }

    function connect() {
      ws = new WebSocket(streamURL());
      ws.onopen = function () {
        wsTries = 0;
        sendWatch();
        // Clean tabs may have drifted while the stream was down; dirty ones
        // keep their buffer and autosave will reconcile.
        tabs.forEach(function (t) {
          if (!t.dirty) wsSend({ type: 'open', path: t.path });
        });
      };
      ws.onmessage = function (ev) {
        console.debug('editor-ws in:', ev.data.slice(0, 160));
        var m;
        try { m = JSON.parse(ev.data); } catch (e) { return; }
        if (m.type === 'content') onContent(m);
        else if (m.type === 'saved') onSaved(m);
        else if (m.type === 'stale') onStale(m);
        else if (m.type === 'deleted') onDeleted(m);
        else if (m.type === 'error') onError(m);
      };
      ws.onclose = function () {
        if (wsTries >= 5) return;
        wsTries += 1;
        wsTimer = setTimeout(connect, Math.min(1000 * Math.pow(2, wsTries), 10000));
      };
    }

    function wsSend(msg) {
      console.debug('editor-ws out:', JSON.stringify(msg).slice(0, 120), 'ready:', ws && ws.readyState);
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
    }

    function sendWatch() {
      wsSend({ type: 'watch', paths: tabs.map(function (t) { return t.path; }) });
    }

    // --- tabs ---

    function tabFor(path) {
      for (var i = 0; i < tabs.length; i++) {
        if (tabs[i].path === path) return tabs[i];
      }
      return null;
    }

    function activeTab() {
      return activePath ? tabFor(activePath) : null;
    }

    function open(path, content, mtime, size, cursor, makeActive) {
      var existing = tabFor(path);
      if (existing) {
        if (makeActive) activate(existing);
        return existing;
      }
      var hostID = 'code-file-' + (++hostSeq);
      var $host = $('<div class="code-file-view" style="flex:1;min-width:0" hidden></div>').attr('id', hostID);
      $stack.append($host);
      var tab = {
        path: path, mtime: mtime, size: size, dirty: false,
        lastSaved: content, timer: null, host: $host[0], hostID: hostID
      };
      var keymap = C.keymap.of([].concat(
        C.searchKeymap, C.defaultKeymap, C.historyKeymap, C.completionKeymap,
        [{ key: 'Mod-s', preventDefault: true, run: function () { flushSave(); return true; } },
         { key: 'Mod-w', preventDefault: true, run: function () { closeTab(tabFor(path)); return true; } }],
        [C.indentWithTab]
      ));
      var langExt = languageFor(path);
      var stateExts = [].concat(theme(), [fontExt().of(fontSpec())], langExt ? [langExt] : [], [
        C.autocompletion(),
        C.closeBrackets(),
        keymap
      ]);
      tab.view = new C.EditorView({
        state: C.EditorState.create({
          doc: content,
          selection: cursor ? { anchor: cursor.anchor, head: cursor.head } : undefined,
          extensions: stateExts
        }),
        parent: $host[0],
        dispatch: function (input, v) {
          var trs = Array.isArray(input) ? input : [input];
          v.update(trs);
          if (trs.some(function (t) { return t.docChanged; })) {
            var at = activeTab();
            if (at && at.view === v) markDirty(at);
          }
        }
      });
      tabs.push(tab);
      renderTabs();
      sendWatch();
      scheduleStateSave();
      if (makeActive || tabs.length === 1) activate(tab);
      return tab;
    }

    function activate(tab) {
      if (!tab) return;
      activePath = tab.path;
      tabs.forEach(function (t) { t.host.hidden = (t !== tab); });
      renderTabs();
      scheduleStateSave();
      if (tab.view) tab.view.focus();
    }

    function closeTab(tab, skipConfirm) {
      if (!tab) return;
      function finish() {
        tabs = tabs.filter(function (t) { return t !== tab; });
        if (tab.host && tab.host.parentNode) tab.host.parentNode.removeChild(tab.host);
        if (activePath === tab.path) activePath = tabs.length ? tabs[tabs.length - 1].path : null;
        var nowActive = activeTab();
        if (nowActive) activate(nowActive);
        renderTabs();
        sendWatch();
        scheduleStateSave();
      }
      if (tab.dirty && !skipConfirm) {
        DS.confirm('Close ' + tab.path + ' with unsaved changes?').then(function (ok) {
          if (ok) finish();
        });
        return;
      }
      finish();
    }

    // --- dirty / autosave ---

    function markDirty(tab) {
      if (!tab || restoring) return;
      tab.dirty = tab.view.state.doc.toString() !== tab.lastSaved;
      renderTabs();
      clearTimeout(tab.timer);
      tab.timer = setTimeout(function () { saveTab(tab); }, 300);
    }

    function saveTab(tab) {
      var doc = tab.view.state.doc.toString();
      tab.lastSent = doc;
      wsSend({
        type: 'save',
        path: tab.path,
        content: doc,
        base_mtime: tab.mtime,
        base_size: tab.size
      });
    }

    function flushSave() {
      var tab = activeTab();
      if (!tab || !tab.dirty) return;
      clearTimeout(tab.timer);
      saveTab(tab);
    }

    function onSaved(m) {
      var tab = tabFor(m.path);
      if (!tab) return;
      tab.mtime = m.mtime;
      tab.size = m.size;
      if (m.type === 'saved') tab.lastSaved = tab.lastSent !== undefined ? tab.lastSent : tab.lastSaved;
      tab.dirty = tab.view.state.doc.toString() !== tab.lastSaved;
      renderTabs();
    }

    function onContent(m) {
      var tab = tabFor(m.path);
      if (!tab) {
        // No tab yet: this is an open reply. Refusals come back as errors.
        if (m.data === undefined || m.data === null) return;
        restoring = false;
        open(m.path, m.data, m.mtime, m.size, null, true);
        return;
      }
      tab.mtime = m.mtime;
      tab.size = m.size;
      var doc = tab.view.state.doc.toString();
      if (doc === m.data) {
        tab.lastSaved = m.data;
        tab.dirty = false;
        renderTabs();
        return;
      }
      if (doc === tab.lastSaved || !tab.dirty) {
        tab.view.dispatch({ changes: { from: 0, to: doc.length, insert: m.data } });
        tab.lastSaved = m.data;
        tab.dirty = false;
        renderTabs();
        return;
      }
      // External change while the local buffer has unsaved keystrokes:
      // conservative prompt; "reload" discards them.
      DS.confirm(m.path + ' changed outside the editor while you have unsaved changes. Discard your changes and reload?').then(function (ok) {
        if (ok) {
          tab.view.dispatch({ changes: { from: 0, to: tab.view.state.doc.length, insert: m.data } });
          tab.lastSaved = m.data;
          tab.dirty = false;
          renderTabs();
        }
      });
    }

    function onStale(m) {
      var tab = tabFor(m.path);
      if (!tab) return;
      tab.mtime = m.mtime;
      tab.size = m.size;
      DS.confirm('Your save to ' + m.path + ' overwrote a newer on-disk version. Restore that version? (Your version is already saved.)').then(function (ok) {
        if (ok) {
          tab.view.dispatch({ changes: { from: 0, to: tab.view.state.doc.length, insert: m.data } });
          tab.lastSaved = m.data;
          tab.dirty = false;
          saveTab(tab);
        } else {
          tab.lastSaved = tab.lastSent;
          tab.dirty = tab.view.state.doc.toString() !== tab.lastSaved;
        }
        renderTabs();
      });
    }

    function onDeleted(m) {
      var tab = tabFor(m.path);
      if (!tab) return;
      if (tab.dirty) {
        DS.confirm(m.path + ' was deleted on disk while it had unsaved changes. Close its tab?').then(function (ok) {
          if (ok) closeTab(tab, true);
        });
        return;
      }
      closeTab(tab, true);
    }

    function onError(m) {
      var msg = m.error || 'Editor error.';
      if (/binary/i.test(msg)) {
        DS.error((m.path || 'This file') + " is binary and can't be edited here. Use the Files tab instead.");
        return;
      }
      if (/bytes; the editor/i.test(msg)) {
        DS.error(msg);
        switchToFiles();
        return;
      }
      DS.error(msg);
    }

    // --- tab strip ---

    function renderTabs() {
      $tabs.empty();
      // .d-flex's !important display beats the [hidden] attribute, so the
      // empty hint is toggled via inline style as well.
      $('#code-empty').prop('hidden', tabs.length > 0)
        .css('display', tabs.length ? 'none' : 'flex');
      tabs.forEach(function (t) {
        var isActive = t.path === activePath;
        // Native Bootstrap 5.3 tab component (bootstrap.Tab), wired via its
        // JavaScript API — see the app-tabs wiring in project.html for why
        // data-bs-toggle (and tabler.min.js's own Tab copy) is avoided.
        var $t = $('<a href="#" class="nav-link code-tab py-1" role="tab" title="' + t.path + '">')
          .attr('data-bs-target', '#' + t.hostID)
          .attr('aria-controls', t.hostID)
          .attr('aria-selected', isActive ? 'true' : 'false')
          .append(document.createTextNode(t.path.split('/').pop()));
        if (t.dirty) $t.append(' <span class="text-warning">●</span>');
        if (isActive) $t.addClass('active');
        $t.on('click', function (e) {
          e.preventDefault();
          bootstrap.Tab.getOrCreateInstance(this).show();
        });
        $t.on('shown.bs.tab', function () { activate(t); });
        var $x = $('<span class="ms-2 text-secondary" role="button" aria-label="Close tab">×</span>');
        $x.on('click', function (e) { e.preventDefault(); e.stopPropagation(); closeTab(t); });
        $t.append($x);
        $tabs.append($t);
      });
    }

    // Ctrl+S anywhere in the pane flushes the active tab's save.
    $('#code-panel').on('keydown.codepane', function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        flushSave();
      }
    });

    // --- tab-state persistence (debounced) ---

    var stateTimer = null;
    function scheduleStateSave() {
      clearTimeout(stateTimer);
      stateTimer = setTimeout(saveState, 1000);
    }

    function saveState() {
      var state = {
        tabs: tabs.map(function (t) {
          var sel = t.view.state.selection.main;
          return { path: t.path, anchor: sel.anchor, head: sel.head };
        }),
        active: activePath || ''
      };
      $.ajax({ url: stateURL, method: 'PUT', contentType: 'application/json', data: JSON.stringify({ state: state }) });
    }

    // --- open flow ---

    function openPath(path) {
      var existing = tabFor(path);
      if (existing) { activate(existing); return; }
      wsSend({ type: 'open', path: path });
    }

    // --- init ---

    connect();

    // Restore persisted tabs: fetch content over the REST files path (the
    // stream's open reply needs a tab to land in, which restore creates via
    // the content push — simpler to just fetch here).
    $.get(stateURL).done(function (resp) {
      var state = resp.state || {};
      var list = (state.tabs || []).filter(function (t) { return t && t.path; });
      if (!list.length) return;
      restoring = true;
      var pending = list.length;
      list.forEach(function (t) {
        $.get('/api/v1/projects/' + projectID + '/files/content', { path: t.path })
          .done(function (content) {
            open(t.path, content, t.head || 0, 0, { anchor: t.anchor, head: t.head }, false);
          })
          .fail(function () { /* file may be gone/binary — skip */ })
          .always(function () {
            if (--pending === 0) {
              restoring = false;
              if (state.active) {
                var t = tabFor(state.active);
                if (t) activate(t);
              }
              renderTabs();
            }
          });
      });
    });

    return {
      open: openPath,
      wsState: function () { return ws ? ws.readyState : 'none'; }
    };
  }
})();