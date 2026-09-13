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
    }
  };
})();