import { t } from './i18n.js';

const PREFIX = '/api/v1';

export class ApiError extends Error {
  constructor(message, code, status, details) {
    super(message);
    this.name = 'ApiError';
    this.code = code || 'error';
    this.status = status || 0;
    this.details = details;
  }
}

let csrfToken = '';
let unauthorizedHandler = null;

export function setCSRF(token) {
  csrfToken = token || '';
}

export function getCSRF() {
  return csrfToken || readCookie('gopanel_csrf');
}

export function onUnauthorized(fn) {
  unauthorizedHandler = fn;
}

function readCookie(name) {
  const match = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/([.*+?^${}()|[\]\\])/g, '\\$1') + '=([^;]*)'));
  return match ? decodeURIComponent(match[1]) : '';
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

function isPublic(path) {
  return path.startsWith('/auth/login') || path.startsWith('/auth/totp') || path.startsWith('/healthz');
}

async function parseResponse(res) {
  const type = res.headers.get('content-type') || '';
  let data = null;
  if (res.status !== 204) {
    if (type.includes('application/json')) {
      try { data = await res.json(); } catch { data = null; }
    } else {
      data = await res.text();
    }
  }
  if (!res.ok) {
    const err = data && data.error ? data.error : {};
    throw new ApiError(err.message || res.statusText || `HTTP ${res.status}`, err.code, res.status, err.details);
  }
  return data;
}

async function request(path, options = {}) {
  const method = (options.method || 'GET').toUpperCase();
  const headers = { Accept: 'application/json', ...(options.headers || {}) };
  let body = options.body;
  if (body !== undefined && body !== null && !(body instanceof FormData) && typeof body !== 'string') {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  if (MUTATING.has(method)) {
    const token = getCSRF();
    if (token) headers['X-CSRF-Token'] = token;
  }
  const res = await fetch(PREFIX + path, {
    method,
    headers,
    body,
    credentials: 'same-origin',
    signal: options.signal,
  });
  if (res.status === 401 && !isPublic(path)) {
    if (unauthorizedHandler) unauthorizedHandler();
    throw new ApiError(t('auth.sessionExpired'), 'auth.required', 401);
  }
  return parseResponse(res);
}

export const api = {
  get(path, options) { return request(path, { ...options, method: 'GET' }); },
  post(path, body, options) { return request(path, { ...options, method: 'POST', body }); },
  put(path, body, options) { return request(path, { ...options, method: 'PUT', body }); },
  patch(path, body, options) { return request(path, { ...options, method: 'PATCH', body }); },
  del(path, body, options) { return request(path, { ...options, method: 'DELETE', body }); },

  upload(path, formData, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open('POST', PREFIX + path);
      xhr.withCredentials = true;
      xhr.setRequestHeader('Accept', 'application/json');
      const token = getCSRF();
      if (token) xhr.setRequestHeader('X-CSRF-Token', token);
      xhr.upload.onprogress = (ev) => {
        if (onProgress && ev.lengthComputable) onProgress(Math.round((ev.loaded / ev.total) * 100));
      };
      xhr.onload = () => {
        let data = null;
        try { data = JSON.parse(xhr.responseText); } catch { data = xhr.responseText; }
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(data);
          return;
        }
        if (xhr.status === 401 && unauthorizedHandler) unauthorizedHandler();
        const err = data && data.error ? data.error : {};
        reject(new ApiError(err.message || `HTTP ${xhr.status}`, err.code, xhr.status, err.details));
      };
      xhr.onerror = () => reject(new ApiError('Network error while uploading', 'network', 0));
      xhr.onabort = () => reject(new ApiError('Upload cancelled', 'aborted', 0));
      xhr.send(formData);
    });
  },

  downloadURL(path) {
    return PREFIX + path;
  },
};

export function qs(params) {
  const sp = new URLSearchParams();
  for (const [key, value] of Object.entries(params || {})) {
    if (value === undefined || value === null || value === '') continue;
    sp.set(key, value);
  }
  const str = sp.toString();
  return str ? `?${str}` : '';
}

export function errorMessage(err) {
  if (!err) return 'Unknown error';
  if (err instanceof ApiError) return err.message;
  if (typeof err === 'string') return err;
  return err.message || String(err);
}
