import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Activity,
  Ban,
  Check,
  Clipboard,
  Clock3,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  ShieldCheck,
  ShieldOff,
  Trash2,
  X,
} from 'lucide-react'
import { externalAccessAPI } from '../api'
import { formatDateTime } from '../i18n'

const EMPTY_FORM = {
  name: '',
  description: '',
  enabled: true,
  permission_codes: [],
  credential_name: '',
  expires_at: '',
}

const RESOURCE_TRANSLATION_KEYS = {
  'POST /api/v1/mailboxes': 'createMailbox',
  'GET /api/v1/mailboxes/:mailbox_ref': 'readMailbox',
  'POST /api/v1/mailboxes/:mailbox_ref/disable': 'disableMailbox',
  'GET /api/v1/orders/:order_id/emails': 'listEmailsByOrder',
  'GET /api/v1/mailboxes/:mailbox_ref/messages': 'listEmailsByMailbox',
  'GET /api/v1/emails/:message_id/body': 'readEmailBody',
  'GET /api/v1/emails/:message_id/attachments/:index': 'downloadAttachment',
  'GET /api/v1/emails/:message_id/raw': 'downloadRawEmail',
}

function permissionName(t, permission) {
  return t(`externalAccess.permissions.${permission.code.replace(':', '_')}`, { defaultValue: permission.name })
}

function resourceName(t, resource) {
  const key = RESOURCE_TRANSLATION_KEYS[`${resource.method} ${resource.path}`]
  return key ? t(`externalAccess.resources.names.${key}`, { defaultValue: resource.name }) : resource.name
}

function toRFC3339(value) {
  return value ? new Date(value).toISOString() : ''
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const timer = setTimeout(onClose, 3000)
    return () => clearTimeout(timer)
  }, [onClose])
  return <div className={`toast toast-${type}`}>{message}</div>
}

function TokenDialog({ token, onClose, onCopied, onCopyError }) {
  const { t } = useTranslation('pages')
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(token)
      onCopied()
    } catch {
      onCopyError()
    }
  }
  return (
    <div className="modal-overlay access-modal-overlay" onClick={onClose}>
      <div className="modal token-modal" onClick={event => event.stopPropagation()} role="dialog" aria-modal="true" aria-label={t('externalAccess.token.aria')}>
        <div className="token-modal-heading">
          <span className="module-icon"><KeyRound size={19} /></span>
          <div><span>Credential issued</span><h3>{t('externalAccess.token.title')}</h3></div>
          <button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button>
        </div>
        <div className="inline-alert token-alert">{t('externalAccess.token.alert')}</div>
        <div className="token-secret"><code>{token}</code></div>
        <div className="modal-footer">
          <button className="btn btn-primary" type="button" onClick={copy}><Clipboard size={16} /> {t('externalAccess.token.copy')}</button>
          <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.done')}</button>
        </div>
      </div>
    </div>
  )
}

function ConfirmDialog({ title, message, confirmLabel, saving = false, onConfirm, onCancel }) {
  const { t } = useTranslation('pages')
  return (
    <div className="modal-overlay access-modal-overlay" onClick={saving ? undefined : onCancel}>
      <div className="modal confirm-modal" onClick={event => event.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" disabled={saving} onClick={onCancel}>{t('common:actions.cancel')}</button>
          <button className="btn btn-danger" type="button" disabled={saving} onClick={onConfirm}>{saving ? t('externalAccess.confirm.processing') : confirmLabel || t('common:actions.confirm')}</button>
        </div>
      </div>
    </div>
  )
}

function PermissionSelector({ permissions, selected, onChange }) {
  const { t } = useTranslation('pages')
  const groups = useMemo(() => permissions.reduce((result, permission) => {
    const group = permission.group_name || t('externalAccess.permissions.other')
    result[group] = result[group] || []
    result[group].push(permission)
    return result
  }, {}), [permissions, t])

  const toggle = (code) => {
    onChange(selected.includes(code) ? selected.filter(item => item !== code) : [...selected, code])
  }

  return (
    <div className="permission-groups">
      {Object.entries(groups).map(([group, items]) => (
        <section className="permission-group" key={group}>
          <div className="permission-group-title">{t(`externalAccess.permissions.groups.${group === '邮箱账号' ? 'mailbox' : group === '邮件读取' ? 'email' : 'unknown'}`, { defaultValue: group })}</div>
          {items.map(permission => (
            <label className={`permission-option ${selected.includes(permission.code) ? 'selected' : ''}`} key={permission.code}>
              <input type="checkbox" checked={selected.includes(permission.code)} onChange={() => toggle(permission.code)} />
              <span className="permission-check"><Check size={14} /></span>
              <span className="permission-option-copy">
                <strong>{permissionName(t, permission)}</strong>
                <code className="permission-code">{permission.code}</code>
                <span className="permission-resource-list">
                  {(permission.resources || []).map(resource => (
                    <span className="permission-resource" key={`${resource.method}-${resource.path}`}>
                      <span className="api-method" data-method={resource.method}>{resource.method}</span>
                      <code>{resource.path}</code>
                    </span>
                  ))}
                </span>
              </span>
            </label>
          ))}
        </section>
      ))}
    </div>
  )
}

function CallableResources({ permissions }) {
  const { t } = useTranslation('pages')
  const resources = useMemo(() => permissions.flatMap(permission =>
    (permission.resources || []).map(resource => ({ ...resource, permission })),
  ), [permissions])

  return (
    <section className="section data-section callable-resources-section">
      <div className="panel-header">
        <div>
          <h3>{t('externalAccess.resources.title')}</h3>
          <div className="panel-caption">{t('externalAccess.resources.caption', { count: resources.length })}</div>
        </div>
      </div>
      <div className="table-wrap">
        <table className="data-table callable-resources-table">
          <thead><tr><th>{t('externalAccess.resources.endpoint')}</th><th>{t('externalAccess.resources.request')}</th><th>{t('externalAccess.resources.permission')}</th></tr></thead>
          <tbody>
            {resources.map(resource => (
              <tr key={`${resource.method}-${resource.path}`}>
                <td><strong>{resourceName(t, resource)}</strong></td>
                <td><div className="api-route"><span className="api-method" data-method={resource.method}>{resource.method}</span><code>{resource.path}</code></div></td>
                <td><div className="resource-permission"><strong>{permissionName(t, resource.permission)}</strong><code>{resource.permission_code}</code></div></td>
              </tr>
            ))}
            {resources.length === 0 && <tr><td colSpan={3}><div className="compact-empty">{t('externalAccess.resources.empty')}</div></td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function ApplicationDrawer({ state, permissions, saving, logs, onChange, onSave, onClose, onIssue, onRevoke, onDelete }) {
  const { t } = useTranslation('pages')
  const { mode, form, application } = state
  const update = (field, value) => onChange({ ...state, form: { ...form, [field]: value } })

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer external-access-drawer" onClick={event => event.stopPropagation()} aria-label={mode === 'create' ? t('externalAccess.drawer.createAria') : t('externalAccess.drawer.editAria')}>
        <div className="drawer-header">
          <div className="drawer-title-with-icon">
            <span className="module-icon"><ShieldCheck size={18} /></span>
            <div><div className="drawer-kicker">External access</div><h2>{mode === 'create' ? t('externalAccess.drawer.createTitle') : form.name}</h2></div>
          </div>
          <button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button>
        </div>

        <form className="drawer-body external-access-form" onSubmit={onSave}>
          <div className="form-group">
            <label>{t('externalAccess.drawer.name')}</label>
            <input value={form.name} onChange={event => update('name', event.target.value)} placeholder={t('externalAccess.drawer.namePlaceholder')} required maxLength={128} />
          </div>
          <div className="form-group">
            <label>{t('externalAccess.drawer.description')}</label>
            <textarea rows={3} value={form.description} onChange={event => update('description', event.target.value)} placeholder={t('externalAccess.drawer.descriptionPlaceholder')} maxLength={512} />
          </div>
          <div className="form-group">
            <label>{t('externalAccess.drawer.status')}</label>
            <label className="switch-row">
              <span className="toggle"><input type="checkbox" checked={form.enabled} onChange={event => update('enabled', event.target.checked)} /><span className="toggle-slider" /></span>
              {form.enabled ? t('common:states.enabled') : t('common:states.disabled')}
            </label>
          </div>
          <div className="form-group">
            <label>{t('externalAccess.drawer.functions')}</label>
            <PermissionSelector permissions={permissions} selected={form.permission_codes} onChange={value => update('permission_codes', value)} />
          </div>

          {mode === 'create' && (
            <div className="credential-issue-fields">
              <div className="form-group"><label>{t('externalAccess.drawer.credentialName')}</label><input value={form.credential_name} onChange={event => update('credential_name', event.target.value)} required /></div>
              <div className="form-group"><label>{t('externalAccess.drawer.expiresAt')}</label><input type="datetime-local" value={form.expires_at} onChange={event => update('expires_at', event.target.value)} /></div>
            </div>
          )}

          {mode === 'edit' && (
            <>
              <section className="access-detail-section">
                <div className="access-detail-heading"><div><h3>{t('externalAccess.drawer.credentials')}</h3><span>{t('externalAccess.drawer.credentialCount', { count: application.credentials?.length || 0 })}</span></div><button className="btn btn-outline btn-sm" type="button" onClick={onIssue}><Plus size={15} /> {t('externalAccess.drawer.issueAndCopy')}</button></div>
                <div className="credential-list">
                  {(application.credentials || []).map(credential => (
                    <div className="credential-row" key={credential.id}>
                      <span className={`credential-state ${credential.enabled ? 'active' : ''}`}><KeyRound size={15} /></span>
                      <div><strong>{credential.name}</strong><code>{credential.token_prefix}...</code><small>{t('externalAccess.drawer.lastUsed', { date: credential.last_used_at ? formatDateTime(credential.last_used_at) : t('externalAccess.defaults.never') })}</small></div>
                      <div className="credential-actions">
                        {!credential.enabled && <span className="status-badge status-down">{t('externalAccess.drawer.revoked')}</span>}
                        {credential.enabled && (
                          <button className="icon-button compact" type="button" title={t('externalAccess.drawer.revokeCredentialTitle')} onClick={() => onRevoke(credential)}><Ban size={15} /></button>
                        )}
                        <button className="icon-button compact danger" type="button" title={t('externalAccess.drawer.deleteCredentialTitle')} onClick={() => onDelete(credential)}><Trash2 size={15} /></button>
                      </div>
                    </div>
                  ))}
                </div>
              </section>
              <section className="access-detail-section">
                <div className="access-detail-heading"><div><h3>{t('externalAccess.drawer.recentCalls')}</h3><span>{t('externalAccess.drawer.logCount', { count: logs.total || 0 })}</span></div></div>
                <div className="access-log-list">
                  {(logs.items || []).slice(0, 8).map(log => (
                    <div className="access-log-row" key={log.id}>
                      <span className={log.status_code < 400 ? 'log-ok' : 'log-error'}>{log.status_code}</span>
                      <div><strong>{log.method} {log.path}</strong><small>{log.permission_code} · {log.client_ip || t('externalAccess.drawer.unknownIp')} · {log.duration_ms} ms</small></div>
                      <time>{formatDateTime(log.created_at)}</time>
                    </div>
                  ))}
                  {(!logs.items || logs.items.length === 0) && <div className="compact-empty">{t('externalAccess.drawer.noLogs')}</div>}
                </div>
              </section>
            </>
          )}

          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button>
            <button className="btn btn-primary" type="submit" disabled={saving || form.permission_codes.length === 0}>{saving && <span className="spinner" />} {t('common:actions.save')}</button>
          </div>
        </form>
      </aside>
    </div>
  )
}

function CredentialDialog({ saving, onSave, onClose }) {
  const { t } = useTranslation('pages')
  const [form, setForm] = useState({ name: t('externalAccess.defaults.productionCredential'), expires_at: '' })
  return (
    <div className="modal-overlay access-modal-overlay" onClick={onClose}>
      <form className="modal credential-modal" onClick={event => event.stopPropagation()} onSubmit={event => { event.preventDefault(); onSave({ name: form.name, expires_at: toRFC3339(form.expires_at) }) }}>
        <h3>{t('externalAccess.credentialDialog.title')}</h3>
        <div className="form-group"><label>{t('externalAccess.drawer.credentialName')}</label><input value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} required /></div>
        <div className="form-group"><label>{t('externalAccess.drawer.expiresAt')}</label><input type="datetime-local" value={form.expires_at} onChange={event => setForm({ ...form, expires_at: event.target.value })} /></div>
        <div className="modal-footer"><button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button><button className="btn btn-primary" type="submit" disabled={saving}>{saving && <span className="spinner" />} {t('externalAccess.credentialDialog.issue')}</button></div>
      </form>
    </div>
  )
}

export default function ExternalAccessPage() {
  const { t } = useTranslation('pages')
  const [applications, setApplications] = useState([])
  const [permissions, setPermissions] = useState([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [drawer, setDrawer] = useState(null)
  const [logs, setLogs] = useState({ items: [], total: 0 })
  const [credentialDialog, setCredentialDialog] = useState(false)
  const [token, setToken] = useState('')
  const [confirm, setConfirm] = useState(null)
  const [toast, setToast] = useState(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const [apps, permissionList] = await Promise.all([externalAccessAPI.list(), externalAccessAPI.permissions()])
      setApplications(Array.isArray(apps) ? apps : [])
      setPermissions(Array.isArray(permissionList) ? permissionList : [])
    } catch (error) {
      setToast({ type: 'error', message: t('common:errors.loadFailed', { message: error.message }) })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => ({
    total: applications.length,
    enabled: applications.filter(item => item.enabled).length,
    credentials: applications.reduce((sum, item) => sum + (item.credentials || []).filter(value => value.enabled).length, 0),
    used: applications.filter(item => item.last_used_at).length,
  }), [applications])

  const openCreate = () => setDrawer({ mode: 'create', form: { ...EMPTY_FORM, credential_name: t('externalAccess.defaults.credential') }, application: null })

  const openEdit = async (application) => {
    try {
      const [detail, accessLogs] = await Promise.all([externalAccessAPI.get(application.id), externalAccessAPI.logs(application.id)])
      setLogs(accessLogs || { items: [], total: 0 })
      setDrawer({
        mode: 'edit',
        application: detail,
        form: { name: detail.name, description: detail.description || '', enabled: detail.enabled, permission_codes: detail.permission_codes || [] },
      })
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    }
  }

  const refreshOpenApplication = async (id) => {
    const [detail, accessLogs] = await Promise.all([externalAccessAPI.get(id), externalAccessAPI.logs(id)])
    setLogs(accessLogs || { items: [], total: 0 })
    setDrawer(current => current ? { ...current, application: detail, form: { ...current.form, name: detail.name, description: detail.description || '', enabled: detail.enabled, permission_codes: detail.permission_codes || [] } } : current)
    await load(true)
  }

  const saveApplication = async (event) => {
    event.preventDefault()
    setSaving(true)
    try {
      const payload = { ...drawer.form, expires_at: toRFC3339(drawer.form.expires_at) }
      if (drawer.mode === 'create') {
        const result = await externalAccessAPI.create(payload)
        setDrawer(null)
        setToken(result.token)
        setToast({ type: 'success', message: t('externalAccess.messages.created') })
      } else {
        await externalAccessAPI.update(drawer.application.id, payload)
        setDrawer(null)
        setToast({ type: 'success', message: t('externalAccess.messages.updated') })
      }
      await load(true)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  const issueCredential = async (form) => {
    setSaving(true)
    try {
      const result = await externalAccessAPI.createCredential(drawer.application.id, form)
      setCredentialDialog(false)
      setToken(result.token)
      await refreshOpenApplication(drawer.application.id)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  const askDelete = (credential) => setConfirm({
    title: t('externalAccess.dialogs.deleteTitle'),
    message: t('externalAccess.dialogs.deleteMessage', { name: credential.name }),
    confirmLabel: t('externalAccess.dialogs.deleteConfirm'),
    onConfirm: async () => {
      setSaving(true)
      try {
        await externalAccessAPI.deleteCredential(drawer.application.id, credential.id)
        await refreshOpenApplication(drawer.application.id)
        setToast({ type: 'success', message: t('externalAccess.messages.deleted') })
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      } finally {
        setSaving(false)
        setConfirm(null)
      }
    },
  })

  const askRevoke = (credential) => setConfirm({
    title: t('externalAccess.dialogs.revokeTitle'),
    message: t('externalAccess.dialogs.revokeMessage', { name: credential.name }),
    confirmLabel: t('externalAccess.dialogs.revokeConfirm'),
    onConfirm: async () => {
      setSaving(true)
      try {
        await externalAccessAPI.revokeCredential(drawer.application.id, credential.id)
        await refreshOpenApplication(drawer.application.id)
        setToast({ type: 'success', message: t('externalAccess.messages.revoked') })
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      } finally {
        setSaving(false)
        setConfirm(null)
      }
    },
  })

  if (loading) return <div className="dashboard-panel loading-panel"><span className="spinner" /> {t('externalAccess.loading')}</div>

  return (
    <div className="external-access-page">
      <div className="page-header">
        <div><h1>{t('externalAccess.title')}</h1><p className="page-subtitle">{t('externalAccess.subtitle')}</p></div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" disabled={refreshing} onClick={() => load(true)}><RefreshCw size={16} className={refreshing ? 'spin' : ''} /> {t('common:actions.refresh')}</button>
          <button className="btn btn-primary" type="button" onClick={openCreate}><Plus size={16} /> {t('externalAccess.add')}</button>
        </div>
      </div>

      <div className="summary-grid">
        <div className="summary-tile" data-tone="brand"><span className="summary-icon"><ShieldCheck size={18} /></span><div><div className="summary-value">{summary.total}</div><div className="summary-label">{t('externalAccess.summary.applications')}</div></div></div>
        <div className="summary-tile" data-tone="success"><span className="summary-icon"><Activity size={18} /></span><div><div className="summary-value">{summary.enabled}</div><div className="summary-label">{t('externalAccess.summary.enabled')}</div></div></div>
        <div className="summary-tile" data-tone="info"><span className="summary-icon"><KeyRound size={18} /></span><div><div className="summary-value">{summary.credentials}</div><div className="summary-label">{t('externalAccess.summary.credentials')}</div></div></div>
        <div className="summary-tile" data-tone="warning"><span className="summary-icon"><Clock3 size={18} /></span><div><div className="summary-value">{summary.used}</div><div className="summary-label">{t('externalAccess.summary.used')}</div></div></div>
      </div>

      <CallableResources permissions={permissions} />

      <section className="section data-section">
        <div className="panel-header"><div><h3>{t('externalAccess.list.title')}</h3><div className="panel-caption">{t('externalAccess.list.caption')}</div></div></div>
        <div className="table-wrap">
          <table className="data-table external-access-table">
            <thead><tr><th>{t('externalAccess.list.application')}</th><th>{t('externalAccess.list.status')}</th><th>{t('externalAccess.list.functions')}</th><th>{t('externalAccess.list.credentials')}</th><th>{t('externalAccess.list.lastUsed')}</th><th>{t('externalAccess.list.operations')}</th></tr></thead>
            <tbody>
              {applications.map(application => (
                <tr key={application.id}>
                  <td><div className="entity-cell"><span className="entity-icon"><ShieldCheck size={16} /></span><div><strong>{application.name}</strong><span>{application.description || `#${application.id}`}</span></div></div></td>
                  <td><span className={`status-badge ${application.enabled ? 'status-active' : 'status-down'}`}>{application.enabled ? t('externalAccess.list.enabled') : t('externalAccess.list.disabled')}</span></td>
                  <td><div className="tag-list">{(application.permission_codes || []).map(code => <span className="tag tag-info" key={code}>{t(`externalAccess.permissions.${code.replace(':', '_')}`, { defaultValue: permissions.find(item => item.code === code)?.name || code })}</span>)}</div></td>
                  <td>{(application.credentials || []).filter(item => item.enabled).length}</td>
                  <td><span className="muted-text">{application.last_used_at ? formatDateTime(application.last_used_at) : t('externalAccess.defaults.never')}</span></td>
                  <td><div className="row-actions"><button className="icon-button compact" type="button" title={t('externalAccess.list.edit')} onClick={() => openEdit(application)}><Pencil size={15} /></button></div></td>
                </tr>
              ))}
              {applications.length === 0 && <tr><td colSpan={6}><div className="empty-state"><ShieldOff size={28} /><strong>{t('externalAccess.list.empty')}</strong><span>{t('externalAccess.list.emptyDesc')}</span><button className="btn btn-primary" type="button" onClick={openCreate}><Plus size={16} /> {t('externalAccess.add')}</button></div></td></tr>}
            </tbody>
          </table>
        </div>
      </section>

      {drawer && <ApplicationDrawer state={drawer} permissions={permissions} saving={saving} logs={logs} onChange={setDrawer} onSave={saveApplication} onClose={() => setDrawer(null)} onIssue={() => setCredentialDialog(true)} onRevoke={askRevoke} onDelete={askDelete} />}
      {credentialDialog && <CredentialDialog saving={saving} onSave={issueCredential} onClose={() => setCredentialDialog(false)} />}
      {token && <TokenDialog token={token} onClose={() => setToken('')} onCopied={() => setToast({ type: 'success', message: t('externalAccess.token.copied') })} onCopyError={() => setToast({ type: 'error', message: t('externalAccess.token.copyFailed') })} />}
      {confirm && <ConfirmDialog {...confirm} saving={saving} onCancel={() => setConfirm(null)} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
