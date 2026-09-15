// Help system: contextual popovers (all pages) + the full manual page
// (help.html only). Content lives as static Markdown with flat frontmatter
// under /help/*.md, listed by /help/index.json — no server/DB involvement
// (docs/WEB_UI_DESIGN.md, ADR-help-system).

(function () {
  'use strict';

  var cache = {}; // id -> {id, title, summary, body}

  function escapeText(s) {
    var d = document.createElement('div');
    d.textContent = s == null ? '' : String(s);
    return d.innerHTML;
  }

  // Frontmatter is flat "key: value" lines between a leading and trailing
  // "---" line — deliberately not YAML, so no parser dependency is needed
  // for the handful of fields help content actually uses.
  function parseFrontmatter(text) {
    var m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(text);
    if (!m) return { meta: {}, body: text };
    var meta = {};
    m[1].split(/\r?\n/).forEach(function (line) {
      var i = line.indexOf(':');
      if (i < 1) return;
      meta[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    });
    return { meta: meta, body: m[2] };
  }

  function loadTopic(id) {
    if (cache[id]) return $.when(cache[id]);
    return $.get('/help/' + id + '.md').then(function (text) {
      var parsed = parseFrontmatter(text);
      var entry = {
        id: id,
        title: parsed.meta.title || id,
        summary: parsed.meta.summary || '',
        body: parsed.body
      };
      cache[id] = entry;
      return entry;
    });
  }

  // ---- Contextual popovers (every page) ----
  // Real bootstrap.Popover, not a hand-rolled one — proper focus/ARIA/
  // Escape/outside-click/positioning behavior for free. Two sources for
  // the class, depending on the page:
  //  - project.html loads the full official bootstrap.bundle.min.js (for
  //    Tab, per ADR-034) — window.bootstrap.Popover is already there.
  //  - Every other page only loads tabler.min.js's own private rebuild of
  //    Bootstrap's JS (window.tabler, not window.bootstrap); loading the
  //    full official bundle there too would reintroduce the exact
  //    Dropdown data-api collision ADR-034 hit (both libraries bind
  //    [data-bs-toggle="dropdown"], used by the shared notifications
  //    bell). Those pages instead load just the Popover dependency chain
  //    (vendor/bootstrap/js/popover/*, official Bootstrap 5.3.8 dist
  //    files) which never touches Dropdown, exposing flat globals
  //    (window.Popover, window.Tooltip, …) instead of window.bootstrap.
  function popoverCtor() {
    if (window.bootstrap && window.bootstrap.Popover) return window.bootstrap.Popover;
    return window.Popover || null;
  }

  function initHelpIcons() {
    var icons = document.querySelectorAll('[data-help]');
    if (!icons.length) return;
    var Ctor = popoverCtor();
    if (!Ctor) return;
    var ids = {};
    for (var i = 0; i < icons.length; i++) ids[icons[i].getAttribute('data-help')] = true;
    var loads = Object.keys(ids).map(loadTopic);
    $.when.apply($, loads).always(function () {
      for (var j = 0; j < icons.length; j++) {
        Ctor.getOrCreateInstance(icons[j], {
          trigger: 'focus click',
          placement: 'auto',
          html: true,
          title: function () {
            var e = cache[this.getAttribute('data-help')];
            return e ? escapeText(e.title) : 'Help';
          },
          content: function () {
            var id = this.getAttribute('data-help');
            var e = cache[id];
            if (!e) return 'Help content unavailable.';
            return escapeText(e.summary) + ' <a href="/help.html#' + encodeURIComponent(id) + '">Read more</a>';
          }
        });
      }
    });
  }

  // ---- Full manual page (help.html only) ----
  function initManualPage() {
    var root = document.getElementById('help-app');
    if (!root) return;

    var $toc = $('#help-toc');
    var $content = $('#help-content');

    function renderToc(ids) {
      $toc.empty();
      ids.forEach(function (id) {
        var e = cache[id];
        $toc.append(
          '<a href="#' + encodeURIComponent(id) + '" class="list-group-item list-group-item-action" data-toc="' + id + '">'
          + escapeText(e ? e.title : id) + '</a>'
        );
      });
    }

    function showTopic(id) {
      var e = cache[id];
      $toc.find('[data-toc]').removeClass('active');
      $toc.find('[data-toc="' + id + '"]').addClass('active');
      if (!e) {
        $content.html('<p class="text-secondary">Topic not found.</p>');
        return;
      }
      var html = marked.parse(e.body);
      $content.empty().append(DOMPurify.sanitize(html, { RETURN_DOM_FRAGMENT: true }));
    }

    $.get('/help/index.json').done(function (ids) {
      $.when.apply($, ids.map(loadTopic)).always(function () {
        renderToc(ids);
        showTopic((location.hash || '').slice(1) || ids[0]);
      });
    });

    $(window).on('hashchange', function () {
      var id = (location.hash || '').slice(1);
      if (id) showTopic(id);
    });

    // Plain substring search over title/summary/body — the doc set is
    // small and single-author, a fuzzy-search library would be premature.
    $('#help-search').on('input', function () {
      var q = $(this).val().trim().toLowerCase();
      $toc.find('[data-toc]').each(function () {
        var e = cache[$(this).data('toc')];
        var hay = (e ? (e.title + ' ' + e.summary + ' ' + e.body) : '').toLowerCase();
        $(this).toggle(!q || hay.indexOf(q) !== -1);
      });
    });
  }

  $(function () {
    initHelpIcons();
    initManualPage();
  });

  // Pages whose data-help can change at runtime (project.html swaps one
  // icon across several topics as its tabs switch) need those topics
  // fetched ahead of the switch — initHelpIcons() only scans data-help
  // values present in the DOM at page load, which is just whichever tab
  // starts active.
  window.DSHelp = {
    ensureTopics: function (ids) { ids.forEach(loadTopic); }
  };
})();
