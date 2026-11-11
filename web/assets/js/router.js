import { loadingBlock } from './ui.js';

export function createRouter({ outlet, routes, onNavigate, onError }) {
  let cleanup = null;
  let token = 0;

  function parse() {
    const raw = location.hash.replace(/^#/, '') || '/dashboard';
    const [pathPart, queryPart] = raw.split('?');
    const path = pathPart.startsWith('/') ? pathPart : `/${pathPart}`;
    return { path: path.replace(/\/+$/, '') || '/dashboard', query: new URLSearchParams(queryPart || '') };
  }

  function match(path) {
    for (const route of routes) {
      if (route.path === path) return { route, params: {} };
      if (route.path.includes(':')) {
        const rp = route.path.split('/');
        const pp = path.split('/');
        if (rp.length !== pp.length) continue;
        const params = {};
        let ok = true;
        for (let i = 0; i < rp.length; i += 1) {
          if (rp[i].startsWith(':')) params[rp[i].slice(1)] = decodeURIComponent(pp[i]);
          else if (rp[i] !== pp[i]) { ok = false; break; }
        }
        if (ok) return { route, params };
      }
    }
    return null;
  }

  async function render() {
    const myToken = ++token;
    if (typeof cleanup === 'function') {
      try { cleanup(); } catch { /* ignore */ }
      cleanup = null;
    }
    const { path, query } = parse();
    const found = match(path);
    if (!found) {
      if (onNavigate) onNavigate(path, null);
      outlet.replaceChildren(loadingBlock('Page not found'));
      return;
    }
    if (onNavigate) onNavigate(path, found.route);
    outlet.replaceChildren(loadingBlock());
    try {
      const result = await found.route.view({
        outlet,
        params: found.params,
        query,
        path,
        setCleanup: (fn) => { cleanup = fn; },
        navigate: (to) => { location.hash = `#${to}`; },
        rerender: () => { render(); },
      });
      if (myToken !== token) return;
      if (result instanceof Node) outlet.replaceChildren(result);
      else if (result && result.el instanceof Node) {
        outlet.replaceChildren(result.el);
        if (typeof result.cleanup === 'function') cleanup = result.cleanup;
      }
      outlet.scrollTop = 0;
      window.scrollTo({ top: 0, behavior: 'instant' });
    } catch (err) {
      if (myToken !== token) return;
      if (onError) onError(err);
    }
  }

  window.addEventListener('hashchange', render);
  return {
    start() {
      if (!location.hash) location.hash = '#/dashboard';
      render();
    },
    render,
    currentPath: () => parse().path,
    stop() {
      window.removeEventListener('hashchange', render);
    },
  };
}

export function navigate(path) {
  const target = `#${path}`;
  if (location.hash === target) {
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  } else {
    location.hash = target;
  }
}
