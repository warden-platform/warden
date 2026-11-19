// Apply theme before paint to avoid flash. Mission Control is dark-first:
// honor a saved preference, otherwise default to the graphite ops theme.
(function () {
  try {
    var saved = localStorage.getItem('warden-theme')
    var dark = saved ? saved === 'dark' : true
    if (dark) document.documentElement.classList.add('dark')
  } catch (_) {
    // Storage can be unavailable in hardened/private browser contexts.
  }
})()