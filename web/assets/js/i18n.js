let current = 'en';
let dict = {};
let fallback = {};
const cache = {};

export function getLang() {
  return current;
}

export async function loadLocale(lang) {
  const target = lang === 'id' ? 'id' : 'en';
  if (!cache[target]) {
    const res = await fetch(`/locales/${target}.json`, { credentials: 'same-origin' });
    if (!res.ok) throw new Error(`Failed to load locale ${target}`);
    cache[target] = await res.json();
  }
  dict = cache[target];
  if (target !== 'en') {
    if (!cache.en) {
      try {
        const res = await fetch('/locales/en.json', { credentials: 'same-origin' });
        cache.en = res.ok ? await res.json() : {};
      } catch {
        cache.en = {};
      }
    }
    fallback = cache.en;
  } else {
    fallback = {};
  }
  current = target;
  document.documentElement.lang = target;
  return target;
}

export function setLocale(lang) {
  try { localStorage.setItem('gopanel.lang', lang); } catch { /* ignore */ }
}

export function savedLocale() {
  try {
    return localStorage.getItem('gopanel.lang') || '';
  } catch {
    return '';
  }
}

export function t(key, vars) {
  let text = dict[key];
  if (text === undefined) text = fallback[key];
  if (text === undefined) text = key;
  if (vars) {
    text = text.replace(/\{(\w+)\}/g, (all, name) => (vars[name] !== undefined ? String(vars[name]) : all));
  }
  return text;
}

export function localeName(lang) {
  return lang === 'id' ? 'Bahasa Indonesia' : 'English';
}
