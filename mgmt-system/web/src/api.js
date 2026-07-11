const API_BASE = '/api/v1/admin';

async function request(path, options = {}) {
  const url = `${API_BASE}${path}`;
  const config = {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  };

  const resp = await fetch(url, config);

  // For attachment downloads, return blob
  if (options._raw) {
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    return resp;
  }

  const data = await resp.json();
  if (data.code !== 0) {
    throw new Error(data.message || '请求失败');
  }
  return data.data;
}

/** 简单 GET 请求（带 query params） */
function get(path, params) {
  const qs = new URLSearchParams();
  if (params) {
    Object.entries(params).forEach(([k, v]) => {
      if (v !== '' && v !== undefined && v !== null) qs.set(k, v);
    });
  }
  const q = qs.toString();
  return request(`${path}${q ? '?' + q : ''}`);
}

// ─── Dashboard ───────────────────────────────────────────
export const dashboardAPI = {
  stats() { return get('/dashboard'); },
};

// ─── Servers ─────────────────────────────────────────────
export const serverAPI = {
  list() { return get('/servers'); },
  create(data) { return request('/servers', { method: 'POST', body: JSON.stringify(data) }); },
  update(id, data) { return request(`/servers/${id}`, { method: 'PUT', body: JSON.stringify(data) }); },
  remove(id) { return request(`/servers/${id}`, { method: 'DELETE' }); },
  domains(id) { return get(`/servers/${id}/domains`); },
  addDomain(id, data) { return request(`/servers/${id}/domains`, { method: 'POST', body: JSON.stringify(data) }); },
  configs(id) { return get(`/servers/${id}/configs`); },
  updateConfig(id, key, value) { return request(`/servers/${id}/configs/${encodeURIComponent(key)}`, { method: 'PUT', body: JSON.stringify({ value: String(value) }) }); },
  resetConfig(id, key) { return request(`/servers/${id}/configs/${encodeURIComponent(key)}`, { method: 'DELETE' }); },
};

// ─── Integrated Mailboxes (转发目标池) ───────────────────
export const integratedMailboxAPI = {
  list() { return get('/integrated-mailboxes'); },
  create(data) { return request('/integrated-mailboxes', { method: 'POST', body: JSON.stringify(data) }); },
  update(id, data) { return request(`/integrated-mailboxes/${id}`, { method: 'PUT', body: JSON.stringify(data) }); },
  remove(id) { return request(`/integrated-mailboxes/${id}`, { method: 'DELETE' }); },
  activate(id) { return request(`/integrated-mailboxes/${id}/activate`, { method: 'POST' }); },
};

// ─── Filters ─────────────────────────────────────────────
export const filterAPI = {
  list() { return get('/filters'); },
  create(data) { return request('/filters', { method: 'POST', body: JSON.stringify(data) }); },
  update(id, data) { return request(`/filters/${id}`, { method: 'PUT', body: JSON.stringify(data) }); },
  remove(id) { return request(`/filters/${id}`, { method: 'DELETE' }); },
};

// ─── Mailboxes ───────────────────────────────────────────
export const mailboxAPI = {
  list(params) { return get('/mailboxes', params); },
  batchCreate(items) { return request('/mailboxes/batch', { method: 'POST', body: JSON.stringify(items) }); },
  updatePassword(id, password) { return request(`/mailboxes/${id}`, { method: 'PUT', body: JSON.stringify({ password }) }); },
  remove(id) { return request(`/mailboxes/${id}/delete`, { method: 'POST' }); },
  restore(id) { return request(`/mailboxes/${id}/restore`, { method: 'POST' }); },
  purge(id) { return request(`/mailboxes/${id}/purge`, { method: 'POST' }); },
};

// ─── Emails ──────────────────────────────────────────────
export const emailAPI = {
  list(mailbox, page, size) { return get('/emails', { mailbox, page, size }); },
  body(id, mailbox) { return get(`/emails/${encodeURIComponent(id)}/body`, { mailbox }); },
  attachmentUrl(id, index, mailbox) {
    return `${API_BASE}/emails/${encodeURIComponent(id)}/attachments/${index}?mailbox=${encodeURIComponent(mailbox)}`;
  },
  attachmentPreviewUrl(id, index, mailbox) {
    return `${API_BASE}/emails/${encodeURIComponent(id)}/attachments/${index}/preview?mailbox=${encodeURIComponent(mailbox)}`;
  },
};

// ─── Domains (for filter dropdowns) ──────────────────────
export const domainAPI = {
  list() { return get('/domains'); },
};

// ─── Config (unchanged) ──────────────────────────────────
export const configAPI = {
  list(category = '') {
    const params = category ? `?category=${encodeURIComponent(category)}` : '';
    return request(`/configs${params}`);
  },
  get(key) { return request(`/configs/${encodeURIComponent(key)}`); },
  update(key, value) {
    return request(`/configs/${encodeURIComponent(key)}`, {
      method: 'PUT',
      body: JSON.stringify({ value: String(value) }),
    });
  },
  batchUpdate(updates) {
    return request('/configs/batch', { method: 'POST', body: JSON.stringify({ updates }) });
  },
  reset(key) {
    return request(`/configs/${encodeURIComponent(key)}/reset`, { method: 'POST' });
  },
  reload() {
    return request('/configs/reload', { method: 'POST' });
  },
};

// ─── Administrator Account ─────────────────────────────
export const accountAPI = {
  get() { return request('/account'); },
  update(data) {
    return request('/account', { method: 'PUT', body: JSON.stringify(data) });
  },
};
