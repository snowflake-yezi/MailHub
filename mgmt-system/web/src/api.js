import i18n from './i18n'

const API_BASE = '/api/v1/admin';

async function request(path, options = {}) {
  const url = `${API_BASE}${path}`;
  const isFormData = options.body instanceof FormData;
  const config = {
    headers: isFormData ? {} : { 'Content-Type': 'application/json' },
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
    throw new Error(data.message || i18n.t('errors.requestFailed'));
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
  get(id) { return get(`/servers/${id}`); },
  create(data) { return request('/servers', { method: 'POST', body: JSON.stringify(data) }); },
  update(id, data) { return request(`/servers/${id}`, { method: 'PUT', body: JSON.stringify(data) }); },
  remove(id) { return request(`/servers/${id}`, { method: 'DELETE' }); },
  domains(id) { return get(`/servers/${id}/domains`); },
  addDomain(id, data) { return request(`/servers/${id}/domains`, { method: 'POST', body: JSON.stringify(data) }); },
  removeDomain(id, domainId) { return request(`/servers/${id}/domains/${domainId}`, { method: 'DELETE' }); },
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

export const filterPolicyAPI = {
  status() { return get('/filter-policy-status'); },
  manualRevisions() { return get('/manual-filter-revisions'); },
  manualRevision(revision) { return get(`/manual-filter-revisions/${revision}`); },
  createManual(data = {}) { return request('/manual-filter-revisions', { method: 'POST', body: JSON.stringify(data) }); },
  replaceManual(revision, rules) { return request(`/manual-filter-revisions/${revision}`, { method: 'PUT', body: JSON.stringify({ rules }) }); },
  validateManual(revision) { return request(`/manual-filter-revisions/${revision}/validate`, { method: 'POST' }); },
  publishManual(revision) { return request(`/manual-filter-revisions/${revision}/publish`, { method: 'POST' }); },
  cloneManual(revision) { return request(`/manual-filter-revisions/${revision}/clone`, { method: 'POST' }); },
  adRevisions() { return get('/ad-filter-revisions'); },
  adRevision(revision) { return get(`/ad-filter-revisions/${revision}`); },
  createAd(data = {}) { return request('/ad-filter-revisions', { method: 'POST', body: JSON.stringify(data) }); },
  updateAdThresholds(revision, tagThreshold, quarantineThreshold) {
    return request(`/ad-filter-revisions/${revision}`, { method: 'PUT', body: JSON.stringify({ tag_threshold: tagThreshold, quarantine_threshold: quarantineThreshold }) });
  },
  addAdDetector(revision, detector) { return request(`/ad-filter-revisions/${revision}/detectors`, { method: 'POST', body: JSON.stringify(detector) }); },
  updateAdDetector(revision, logicalId, detector) { return request(`/ad-filter-revisions/${revision}/detectors/${encodeURIComponent(logicalId)}`, { method: 'PUT', body: JSON.stringify(detector) }); },
  removeAdDetector(revision, logicalId) { return request(`/ad-filter-revisions/${revision}/detectors/${encodeURIComponent(logicalId)}`, { method: 'DELETE' }); },
  addAdComposite(revision, composite) { return request(`/ad-filter-revisions/${revision}/composites`, { method: 'POST', body: JSON.stringify(composite) }); },
  updateAdComposite(revision, logicalId, composite) { return request(`/ad-filter-revisions/${revision}/composites/${encodeURIComponent(logicalId)}`, { method: 'PUT', body: JSON.stringify(composite) }); },
  removeAdComposite(revision, logicalId) { return request(`/ad-filter-revisions/${revision}/composites/${encodeURIComponent(logicalId)}`, { method: 'DELETE' }); },
  putAdWeight(revision, symbol, score) { return request(`/ad-filter-revisions/${revision}/weights/${encodeURIComponent(symbol)}`, { method: 'PUT', body: JSON.stringify({ score }) }); },
  removeAdWeight(revision, symbol) { return request(`/ad-filter-revisions/${revision}/weights/${encodeURIComponent(symbol)}`, { method: 'DELETE' }); },
  validateAd(revision) { return request(`/ad-filter-revisions/${revision}/validate`, { method: 'POST' }); },
  publishAd(revision) { return request(`/ad-filter-revisions/${revision}/publish`, { method: 'POST' }); },
  cloneAd(revision) { return request(`/ad-filter-revisions/${revision}/clone`, { method: 'POST' }); },
  decisions(params = {}) { return get('/filter-decisions', params); },
  decision(key) { return get(`/filter-decisions/${encodeURIComponent(key)}`); },
};

// ─── Mailboxes ───────────────────────────────────────────
export const mailboxAPI = {
  list(params) { return get('/mailboxes', params); },
  batchCreate(items) { return request('/mailboxes/batch', { method: 'POST', body: JSON.stringify(items) }); },
  upload(file, serverId = 0, domainId = 0) {
    const form = new FormData();
    form.append('file', file);
    if (serverId) form.append('server_id', String(serverId));
    if (domainId) form.append('domain_id', String(domainId));
    return request('/mailboxes/upload', { method: 'POST', body: form });
  },
  updatePassword(id, password) { return request(`/mailboxes/${id}`, { method: 'PUT', body: JSON.stringify({ password }) }); },
  remove(id) { return request(`/mailboxes/${id}/delete`, { method: 'POST' }); },
  restore(id) { return request(`/mailboxes/${id}/restore`, { method: 'POST' }); },
  purge(id) { return request(`/mailboxes/${id}/purge`, { method: 'POST' }); },
};

// ─── Emails ──────────────────────────────────────────────
export const emailAPI = {
  list(mailbox, page, size) { return get('/emails', { mailbox, page, size }); },
  body(id, mailbox) { return get(`/emails/${encodeURIComponent(id)}/body`, { mailbox }); },
  remove(id, mailbox) { return request(`/emails/${encodeURIComponent(id)}?mailbox=${encodeURIComponent(mailbox)}`, { method: 'DELETE' }); },
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

// ─── External API applications ──────────────────────────
export const externalAccessAPI = {
  permissions() { return get('/api-permissions'); },
  list() { return get('/external-applications'); },
  get(id) { return get(`/external-applications/${id}`); },
  create(data) { return request('/external-applications', { method: 'POST', body: JSON.stringify(data) }); },
  update(id, data) { return request(`/external-applications/${id}`, { method: 'PUT', body: JSON.stringify(data) }); },
  createCredential(id, data) { return request(`/external-applications/${id}/credentials`, { method: 'POST', body: JSON.stringify(data) }); },
  revokeCredential(id, credentialId) { return request(`/external-applications/${id}/credentials/${credentialId}/revoke`, { method: 'POST' }); },
  deleteCredential(id, credentialId) { return request(`/external-applications/${id}/credentials/${credentialId}`, { method: 'DELETE' }); },
  logs(id, page = 1, size = 20) { return get(`/external-applications/${id}/logs`, { page, size }); },
};
