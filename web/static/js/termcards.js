// DevStationOne "cards" terminal render mode (Slice 17): a drop-in
// alternative to the xterm.js `Terminal` used by web/static/project.html's
// `paneFor()`. Same object shape (write/onData/focus/dispose/options) so
// the terminal-session wiring in project.html does not need to branch on
// render mode anywhere except at pane creation.
//
// ponytail: regex/idle-timer detection over plain (ANSI-stripped) text, not
// a real output-format parser — add a dedicated adapter (e.g. Claude
// Code's --output-format stream-json) when one specific tool's fidelity
// matters enough to justify it.
(function () {
  'use strict';

  // CSI sequences (colors, cursor moves) plus DECKPAM/DECKPNM (`ESC =` / `ESC >`),
  // which `less` sends on entry/exit even for a one-screen diff that never
  // pages — seen for real running `git diff` through cards mode (Slice 17
  // smoke test). Not a full VT100 parser; broaden here if another bare
  // (non-CSI) escape shows up in practice.
  var ANSI_RE = /\x1b\[[0-9;?]*[a-zA-Z]|\x1b[=>]/g;
  // Cursor-repositioning / alt-screen / clear-screen sequences: the
  // signature of a full-screen TUI (vim, htop, fzf, inquirer arrow menus).
  // Detection never attempts to render these as cards (docs/IMPLEMENTATION_PLAN.md
  // Slice 17 non-goal) — the session just falls back to plain text for good.
  var FULLSCREEN_RE = /\x1b\[\?1049h|\x1b\[2J|\x1b\[\d*;?\d*H/;
  var YESNO_RE = /(\(y\/n\)|\[y\/n\]|\(yes\/no\))\s*$/i;
  var MENU_LINE_RE = /^\s*\d+[).]\s+.+$/;
  var FREETEXT_PROMPT_RE = /[:>]\s*$/;
  var DIFF_START_RE = /^diff --git /;
  var IDLE_MS = 150;
  var MAX_RECENT_LINES = 6;

  function stripAnsi(s) {
    return s.replace(ANSI_RE, '');
  }

  function create(pane) {
    var $pane = $(pane).addClass('ds-cards-pane');
    var $scroll = $('<div class="ds-cards-scrollback">').appendTo($pane);
    var $inputRow = $('<div class="ds-cards-input-row input-group input-group-sm">').appendTo($pane);
    var $inputLabel = $('<span class="input-group-text ds-cards-input-label">$</span>').appendTo($inputRow);
    var $input = $('<input type="text" class="form-control" autocomplete="off" spellcheck="false">').appendTo($inputRow);

    var decoder = new TextDecoder();
    var pendingLine = '';
    var idleTimer = null;
    var bailed = false;
    var recentLines = [];
    var diffCard = null; // { $body } while a diff --git block is being collected
    var dataCb = null;

    function scrollToBottom() {
      $scroll.scrollTop($scroll[0].scrollHeight);
    }

    function appendPlainLine(text) {
      $('<div class="ds-cards-line">').text(text).appendTo($scroll);
      scrollToBottom();
    }

    function appendCard($card) {
      $card.appendTo($scroll);
      scrollToBottom();
    }

    function pushRecent(line) {
      recentLines.push(line);
      if (recentLines.length > MAX_RECENT_LINES) recentLines.shift();
    }

    function sendReply(text) {
      if (dataCb) dataCb(text + '\r');
    }

    function renderYesNoCard(promptText) {
      var $card = $('<div class="card card-sm ds-card mb-2">');
      var $body = $('<div class="card-body">').appendTo($card);
      $('<div class="mb-2 text-secondary">').text(promptText).appendTo($body);
      var $buttons = $('<div class="btn-list">').appendTo($body);
      $('<button type="button" class="btn btn-sm btn-success">Yes</button>').on('click', function () {
        $card.addClass('ds-card-done');
        sendReply('y');
      }).appendTo($buttons);
      $('<button type="button" class="btn btn-sm btn-secondary">No</button>').on('click', function () {
        $card.addClass('ds-card-done');
        sendReply('n');
      }).appendTo($buttons);
      appendCard($card);
    }

    function renderMenuCard(lines, promptText) {
      var $card = $('<div class="card card-sm ds-card mb-2">');
      var $body = $('<div class="card-body">').appendTo($card);
      $('<div class="mb-2 text-secondary">').text(promptText || 'Choose an option:').appendTo($body);
      var $list = $('<div class="btn-list">').appendTo($body);
      lines.forEach(function (line) {
        var m = line.match(/^\s*(\d+)[).]\s+(.+)$/);
        if (!m) return;
        $('<button type="button" class="btn btn-sm btn-outline-primary">').text(m[1] + '. ' + m[2]).on('click', function () {
          $card.addClass('ds-card-done');
          sendReply(m[1]);
        }).appendTo($list);
      });
      appendCard($card);
    }

    function startDiffCard() {
      var $card = $('<div class="card card-sm ds-card mb-2">');
      var $header = $('<div class="card-header d-flex justify-content-between align-items-center py-1">').appendTo($card);
      $('<span class="text-secondary"><i class="fa-solid fa-code-branch me-1" aria-hidden="true"></i>Diff</span>').appendTo($header);
      var $body = $('<pre class="ds-cards-diff-body mb-0 p-2">');
      var $toggle = $('<button type="button" class="btn btn-sm btn-ghost-secondary">Collapse</button>').appendTo($header);
      $toggle.on('click', function () {
        $body.toggle();
        $toggle.text($body.is(':visible') ? 'Collapse' : 'Expand');
      });
      $card.append($body);
      appendCard($card);
      diffCard = { $body: $body };
    }

    function feedDiffLine(line) {
      diffCard.$body.append(document.createTextNode(line + '\n'));
      scrollToBottom();
    }

    function processCompleteLine(rawLine) {
      var line = stripAnsi(rawLine);
      if (bailed) {
        appendPlainLine(line);
        return;
      }

      if (diffCard) {
        if (line === '' || DIFF_START_RE.test(line)) {
          diffCard = null;
        } else {
          feedDiffLine(line);
          return;
        }
      }

      if (DIFF_START_RE.test(line)) {
        startDiffCard();
        feedDiffLine(line);
        return;
      }

      appendPlainLine(line);
      pushRecent(line);
    }

    function evaluatePending() {
      idleTimer = null;
      var raw = pendingLine;
      pendingLine = '';
      if (raw === '') return;
      var line = stripAnsi(raw);

      if (bailed) {
        appendPlainLine(line);
        return;
      }

      if (YESNO_RE.test(line)) {
        renderYesNoCard(line);
        recentLines = [];
        return;
      }

      var menuLines = recentLines.filter(function (l) { return MENU_LINE_RE.test(l); });
      if (menuLines.length >= 2) {
        renderMenuCard(menuLines, line);
        recentLines = [];
        return;
      }

      if (FREETEXT_PROMPT_RE.test(line)) {
        $inputLabel.text(line);
        appendPlainLine(line);
        recentLines = [];
        return;
      }

      appendPlainLine(line);
      pushRecent(line);
    }

    function write(data) {
      var text = typeof data === 'string' ? data : decoder.decode(data, { stream: true });
      // PTY output uses \r\n (onlcr): drop bare \r everywhere before line-splitting,
      // since a trailing \r otherwise breaks every $-anchored detector regex below
      // (JS treats \r as a line terminator, so "." and "$" both stop short of it).
      text = text.replace(/\r/g, '');

      if (!bailed && FULLSCREEN_RE.test(text)) {
        bailed = true;
        diffCard = null;
        appendPlainLine('[full-screen terminal output — open a new xterm-mode session to interact with it]');
      }

      var combined = pendingLine + text;
      var lines = combined.split('\n');
      pendingLine = lines.pop();
      lines.forEach(processCompleteLine);

      if (idleTimer) clearTimeout(idleTimer);
      if (pendingLine !== '') idleTimer = setTimeout(evaluatePending, IDLE_MS);
    }

    $input.on('keydown', function (e) {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      var value = $input.val();
      $input.val('');
      $inputLabel.text('$');
      if (dataCb) dataCb(value + '\r');
    });

    return {
      options: {}, // unused by cards mode; keeps the shared font-preference sync in project.html from throwing
      write: write,
      onData: function (cb) { dataCb = cb; },
      focus: function () { $input.trigger('focus'); },
      dispose: function () { $pane.empty(); }
    };
  }

  window.DS = window.DS || {};
  window.DS.termCards = { create: create };
})();
