// Dark mode for the landing page. Lotus Docs only implements dark mode for
// docs pages (assets/docs/js/darkmode-init.js); the landing template never
// loads it. This mirrors the same mechanism: the 'theme' localStorage key
// and the data-dark-mode attribute, so the preference set by the docs
// pages' toggle carries over to the landing and vice versa.
(function () {
  var prefersDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)');
  var stored = localStorage.getItem('theme');
  var dark = stored === 'dark' || (stored === null && prefersDark && prefersDark.matches);
  if (dark) {
    if (stored === null) localStorage.setItem('theme', 'dark');
    document.documentElement.setAttribute('data-dark-mode', '');
  }
  if (prefersDark) {
    prefersDark.addEventListener('change', function (e) {
      localStorage.setItem('theme', e.matches ? 'dark' : 'light');
      document.documentElement.toggleAttribute('data-dark-mode', e.matches);
    });
  }
})();
