'use strict';

// Runs before styles load so a saved dark theme does not flash light on reload.
(() => {
  const key = 'gometer.theme.v1';
  const system = window.matchMedia('(prefers-color-scheme: dark)');
  let preference = 'system';
  try {
    const saved = localStorage.getItem(key);
    if (saved === 'light' || saved === 'dark') preference = saved;
  } catch { /* The theme still works for this visit when storage is unavailable. */ }

  function apply() {
    document.documentElement.dataset.theme = preference === 'system' ? (system.matches ? 'dark' : 'light') : preference;
    window.dispatchEvent(new Event('gometer:themechange'));
  }
  apply();
  system.addEventListener('change', () => { if (preference === 'system') apply(); });

  document.addEventListener('DOMContentLoaded', () => {
    const picker = document.getElementById('theme-select');
    picker.value = preference;
    picker.addEventListener('change', () => {
      preference = picker.value;
      try {
        if (preference === 'system') localStorage.removeItem(key);
        else localStorage.setItem(key, preference);
      } catch { /* Keep the selected theme for this visit. */ }
      apply();
    });
  });
})();
