'use strict';

// Desktop layout is a browser preference, independent of scenario configuration.
(() => {
  const key = 'gometer.sidebar.collapsed.v1';
  const root = document.documentElement;
  let collapsed = false;
  try { collapsed = localStorage.getItem(key) === 'true'; } catch { /* Keep an in-memory preference. */ }
  root.dataset.sidebar = collapsed ? 'collapsed' : 'expanded';

  document.addEventListener('DOMContentLoaded', () => {
    const sidebar = document.getElementById('workspace-sidebar');
    const main = document.getElementById('workspace-main');
    const toggle = document.getElementById('sidebar-toggle');
    const opener = document.getElementById('navigation-open');
    const backdrop = document.getElementById('navigation-backdrop');
    const tooltip = document.getElementById('navigation-tooltip');
    const mobile = window.matchMedia('(max-width:900px)');
    let open = false, tooltipOwner = null;

    function hideTooltip() {
      tooltip.hidden = true;
      if (tooltipOwner) tooltipOwner.removeAttribute('aria-describedby');
      tooltipOwner = null;
    }
    function showTooltip(target) {
      if (mobile.matches || !collapsed || !target) return;
      hideTooltip();
      tooltipOwner = target;
      tooltip.textContent = target.dataset.navTooltip;
      tooltip.hidden = false;
      const bounds = target.getBoundingClientRect();
      tooltip.style.left = `${sidebar.getBoundingClientRect().right + 8}px`;
      tooltip.style.top = `${Math.max(8, Math.min(innerHeight - tooltip.offsetHeight - 8, bounds.top + (bounds.height - tooltip.offsetHeight) / 2))}px`;
      target.setAttribute('aria-describedby', tooltip.id);
    }
    function render() {
      root.dataset.sidebar = collapsed ? 'collapsed' : 'expanded';
      root.dataset.navigationOpen = String(mobile.matches && open);
      toggle.setAttribute('aria-expanded', String(mobile.matches ? open : !collapsed));
      const label = mobile.matches ? 'Close navigation' : collapsed ? 'Expand sidebar' : 'Collapse sidebar';
      toggle.setAttribute('aria-label', label);
      toggle.dataset.navTooltip = label;
      opener.setAttribute('aria-expanded', String(mobile.matches && open));
      backdrop.hidden = !mobile.matches || !open;
      main.inert = mobile.matches && open;
      sidebar.inert = mobile.matches && !open;
      if (mobile.matches && open) {
        sidebar.setAttribute('role', 'dialog');
        sidebar.setAttribute('aria-modal', 'true');
      } else {
        sidebar.removeAttribute('role');
        sidebar.removeAttribute('aria-modal');
      }
      hideTooltip();
      // Resize consumers can remeasure canvases; no editor/model state is rebuilt.
      window.dispatchEvent(new Event('gometer:layoutchange'));
    }
    function closeDrawer(restoreFocus = true) {
      if (!open) return;
      open = false;
      render();
      if (restoreFocus && mobile.matches) opener.focus();
    }
    toggle.addEventListener('click', () => {
      if (mobile.matches) return closeDrawer();
      collapsed = !collapsed;
      try { localStorage.setItem(key, String(collapsed)); } catch { /* Layout still works without storage. */ }
      render();
    });
    opener.addEventListener('click', () => {
      open = true;
      render();
      toggle.focus();
    });
    backdrop.addEventListener('click', () => closeDrawer());
    sidebar.addEventListener('click', event => {
      // Let the shortcut dialog receive focus after the navigation drawer closes.
      if (event.target.closest('[data-shortcuts]')) closeDrawer();
    }, true);
    sidebar.addEventListener('pointerover', event => showTooltip(event.target.closest('[data-nav-tooltip]')));
    sidebar.addEventListener('pointerout', event => {
      if (tooltipOwner && !tooltipOwner.contains(event.relatedTarget)) hideTooltip();
    });
    sidebar.addEventListener('focusin', event => showTooltip(event.target.closest('[data-nav-tooltip]')));
    sidebar.addEventListener('focusout', hideTooltip);
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        hideTooltip();
        if (mobile.matches && open) { event.preventDefault(); closeDrawer(); }
      }
      if (event.key !== 'Tab' || !mobile.matches || !open) return;
      const items = [...sidebar.querySelectorAll('button:not([disabled]), a[href], select')].filter(e => e.getClientRects().length);
      const first = items[0], last = items.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    });
    document.addEventListener('gometer:viewchange', event => {
      const label = {build:'Build',jmx:'JMeter import',live:'Live',history:'History',guide:'Guide',coverage:'Coverage'}[event.detail];
      document.getElementById('current-view-label').textContent = label || 'Workspace';
      if (mobile.matches && open) {
        closeDrawer(false);
        const heading = main.querySelector('.view:not([hidden]) h1');
        (heading || main).setAttribute('tabindex', '-1');
        (heading || main).focus({preventScroll:true});
      }
    });
    mobile.addEventListener('change', () => {
      open = false;
      render();
      if (mobile.matches && sidebar.contains(document.activeElement)) opener.focus();
      else if (!mobile.matches && document.activeElement === opener) toggle.focus();
    });
    window.addEventListener('resize', hideTooltip);
    sidebar.addEventListener('scroll', hideTooltip);
    render();
  });
})();
