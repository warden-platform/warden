// Apply theme before paint to avoid flash. Honors saved preference,
// otherwise falls back to the OS color scheme.
(function () {
  try {
    var saved = localStorage.getItem('pam-theme')
    var dark = saved ? saved === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches
    if (dark) document.documentElement.classList.add('dark')
  } catch (_) {
    // Storage can be unavailable in hardened/private browser contexts.
  }
})()
