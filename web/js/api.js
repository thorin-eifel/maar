// API-Client: Sitzung, CSRF-Token, Fehlerbehandlung.

export const session = { user: null, csrf: null, hostname: '', demo: false, version: '' };

export class ApiError extends Error {
  constructor(status, body) {
    super(body.error || `HTTP ${status}`);
    this.status = status;
    this.check = body.check;
    this.output = body.output;
    this.demo = body.demo;
  }
}

let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn) { onUnauthorized = fn; }

export async function api(method, path, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' };
  if (method !== 'GET') opt.headers['X-CSRF-Token'] = session.csrf || '';
  if (body !== undefined) {
    if (body instanceof Blob) {
      opt.body = body;
      opt.headers['Content-Type'] = 'application/octet-stream';
    } else {
      opt.body = JSON.stringify(body);
      opt.headers['Content-Type'] = 'application/json';
    }
  }
  const res = await fetch(path, opt);
  if (res.status === 204) return null;
  let data = {};
  try { data = await res.json(); } catch { /* leer */ }
  if (res.status === 401 && path !== '/api/login' && path !== '/api/session') onUnauthorized();
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

export const get = p => api('GET', p);
export const post = (p, b) => api('POST', p, b);
export const put = (p, b) => api('PUT', p, b);
export const del = p => api('DELETE', p);

export function q(params) {
  return '?' + new URLSearchParams(params).toString();
}

// Upload mit Fortschritt (für ISO-Dateien).
export function upload(path, file, onProgress) {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open('PUT', path);
    x.setRequestHeader('X-CSRF-Token', session.csrf || '');
    x.setRequestHeader('Content-Type', 'application/octet-stream');
    x.upload.onprogress = e => e.lengthComputable && onProgress(e.loaded / e.total);
    x.onload = () => {
      let d = {};
      try { d = JSON.parse(x.responseText); } catch { /* leer */ }
      x.status < 300 ? resolve(d) : reject(new ApiError(x.status, d));
    };
    x.onerror = () => reject(new Error('Upload abgebrochen'));
    x.send(file);
  });
}

export function wsURL(path) {
  return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + path;
}
