import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Copy,
  Globe2,
  Inbox,
  MailPlus,
  Plus,
  RefreshCw,
  Server,
  Trash2,
  X,
} from 'lucide-react'
import { serverAPI } from '../api'
import { formatDateTime } from '../i18n'

const STATUS_CLASS = {
  active: 'tag-success',
  inactive: 'tag-info',
  synced: 'tag-success',
  partial: 'tag-warning',
  pending: 'tag-info',
  sync_failed: 'tag-danger',
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const timer = setTimeout(onClose, 3000)
    return () => clearTimeout(timer)
  }, [onClose])

  return <div className={`toast toast-${type}`}>{message}</div>
}

function ConfirmDialog({ title, message, confirmLabel, saving, onConfirm, onCancel }) {
  const { t } = useTranslation('common')
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal confirm-modal" onClick={event => event.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" disabled={saving} onClick={onCancel}>{t('actions.cancel')}</button>
          <button className="btn btn-danger" type="button" disabled={saving} onClick={onConfirm}>
            {saving && <span className="spinner" />} {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

function SummaryTile({ icon: Icon, label, value, tone }) {
  return (
    <div className="summary-tile" data-tone={tone}>
      <span className="summary-icon"><Icon size={18} /></span>
      <div>
        <div className="summary-value">{value}</div>
        <div className="summary-label">{label}</div>
      </div>
    </div>
  )
}

function StatusTag({ status }) {
  const { t } = useTranslation('pages')
  return (
    <span className={`tag ${STATUS_CLASS[status] || 'tag-info'}`}>
      {t(`domains.status.${status}`, { defaultValue: status || '-' })}
    </span>
  )
}

async function copyText(value) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  document.body.removeChild(textarea)
  if (!copied) throw new Error('copy failed')
}

export default function ServerDomainsPage() {
  const { t } = useTranslation('pages')
  const navigate = useNavigate()
  const { id } = useParams()
  const serverId = Number.parseInt(id, 10)
  const [server, setServer] = useState(null)
  const [bindings, setBindings] = useState([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [form, setForm] = useState({ name: '', a_record_host: 'mail' })
  const [dnsResult, setDNSResult] = useState(null)

  const load = useCallback(async (silent = false) => {
    if (!Number.isInteger(serverId) || serverId <= 0) {
      setLoading(false)
      setToast({ type: 'error', message: t('domains.messages.invalidServer') })
      return
    }
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const [serverData, domainData] = await Promise.all([
        serverAPI.get(serverId),
        serverAPI.domains(serverId),
      ])
      setServer(serverData)
      setBindings(Array.isArray(domainData) ? domainData : [])
    } catch (error) {
      setToast({ type: 'error', message: t('domains.messages.loadFailed', { message: error.message }) })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [serverId, t])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => {
    const active = bindings.filter(binding => binding.status === 'active')
    return {
      active: active.length,
      ready: active.filter(binding => binding.postfix_status === 'synced').length,
      attention: active.filter(binding => binding.postfix_status !== 'synced' || binding.dkim_status !== 'synced').length,
      mailboxes: bindings.reduce((total, binding) => total + (Number(binding.mailbox_count) || 0), 0),
    }
  }, [bindings])

  const handleAdd = async event => {
    event.preventDefault()
    const name = form.name.trim()
    if (!name) return
    setSaving(true)
    try {
      const result = await serverAPI.addDomain(serverId, {
        name,
        a_record_host: form.a_record_host.trim(),
      })
      setDNSResult({
        domain: result?.domain?.name || name,
        records: result?.setup?.dns_records || [],
      })
      setForm({ name: '', a_record_host: 'mail' })
      setToast({ type: 'success', message: t('domains.messages.added', { name }) })
      await load(true)
    } catch (error) {
      setToast({ type: 'error', message: t('domains.messages.addFailed', { message: error.message }) })
    } finally {
      setSaving(false)
    }
  }

  const askRemove = binding => {
    const name = binding.domain?.name || `#${binding.domain_id}`
    setConfirm({
      binding,
      title: t('domains.dialogs.removeTitle'),
      message: t('domains.dialogs.removeMessage', { name, server: server?.name || `#${serverId}` }),
    })
  }

  const removeDomain = async () => {
    if (!confirm) return
    setRemoving(true)
    try {
      await serverAPI.removeDomain(serverId, confirm.binding.domain_id)
      setToast({ type: 'success', message: t('domains.messages.removed') })
      setConfirm(null)
      await load(true)
    } catch (error) {
      setToast({ type: 'error', message: t('domains.messages.removeFailed', { message: error.message }) })
    } finally {
      setRemoving(false)
    }
  }

  const handleCopy = async value => {
    try {
      await copyText(value)
      setToast({ type: 'success', message: t('domains.messages.copied') })
    } catch {
      setToast({ type: 'error', message: t('domains.messages.copyFailed') })
    }
  }

  const openMailboxCreate = binding => {
    navigate(`/mailboxes?view=create&server_id=${serverId}&domain_id=${binding.domain_id}`)
  }

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> {t('domains.loading')}
      </div>
    )
  }

  if (!server) {
    return (
      <section className="section">
        <div className="empty-state">
          <AlertTriangle size={28} />
          <strong>{t('domains.empty.serverMissing')}</strong>
          <button className="btn btn-outline" type="button" onClick={() => navigate('/servers')}>
            <ArrowLeft size={16} /> {t('domains.back')}
          </button>
        </div>
        {toast && <Toast {...toast} onClose={() => setToast(null)} />}
      </section>
    )
  }

  return (
    <div className="domain-pool-page">
      <div className="page-header">
        <div>
          <button className="back-link-button" type="button" onClick={() => navigate('/servers')}>
            <ArrowLeft size={15} /> {t('domains.back')}
          </button>
          <h1>{t('domains.title', { name: server.name || `#${server.id}` })}</h1>
          <p className="page-subtitle">{t('domains.subtitle')}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" disabled={refreshing} onClick={() => load(true)}>
            {refreshing ? <span className="spinner" /> : <RefreshCw size={16} />}
            {t('common:actions.refresh')}
          </button>
        </div>
      </div>

      <div className="domain-server-strip">
        <span className="entity-icon"><Server size={17} /></span>
        <div>
          <strong>{server.name || `server-${server.id}`}</strong>
          <code>{server.api_host || '-'}</code>
        </div>
        <span className={`status-badge status-${server.status}`}>{t(`servers.status.${server.status}`, { defaultValue: server.status })}</span>
      </div>

      <div className="summary-grid domain-summary-grid">
        <SummaryTile icon={Globe2} label={t('domains.summary.active')} value={summary.active} tone="brand" />
        <SummaryTile icon={CheckCircle2} label={t('domains.summary.ready')} value={summary.ready} tone="success" />
        <SummaryTile icon={AlertTriangle} label={t('domains.summary.attention')} value={summary.attention} tone="warning" />
        <SummaryTile icon={Inbox} label={t('domains.summary.mailboxes')} value={summary.mailboxes} tone="info" />
      </div>

      <section className="section domain-provision-section">
        <div className="panel-header">
          <div>
            <h3>{t('domains.add.title')}</h3>
            <div className="panel-caption">{t('domains.add.caption')}</div>
          </div>
        </div>
        <form className="domain-add-form" onSubmit={handleAdd}>
          <div className="form-group">
            <label htmlFor="domain-name">{t('domains.add.name')}</label>
            <input
              id="domain-name"
              value={form.name}
              placeholder="example.com"
              required
              onChange={event => setForm(current => ({ ...current, name: event.target.value }))}
            />
          </div>
          <div className="form-group">
            <label htmlFor="domain-host">{t('domains.add.host')}</label>
            <input
              id="domain-host"
              value={form.a_record_host}
              placeholder="mail"
              onChange={event => setForm(current => ({ ...current, a_record_host: event.target.value }))}
            />
          </div>
          <button className="btn btn-primary domain-add-button" type="submit" disabled={saving || !form.name.trim()}>
            {saving ? <span className="spinner" /> : <Plus size={16} />}
            {t('domains.add.submit')}
          </button>
        </form>

        {dnsResult && (
          <div className="dns-result" aria-live="polite">
            <div className="dns-result-header">
              <div>
                <strong>{t('domains.dns.title', { name: dnsResult.domain })}</strong>
                <span>{t('domains.dns.count', { count: dnsResult.records.length })}</span>
              </div>
              <button className="icon-button compact" type="button" title={t('common:actions.close')} onClick={() => setDNSResult(null)}>
                <X size={15} />
              </button>
            </div>
            <div className="table-wrap">
              <table className="data-table dns-record-table">
                <thead>
                  <tr>
                    <th>{t('domains.dns.type')}</th>
                    <th>{t('domains.dns.host')}</th>
                    <th>{t('domains.dns.value')}</th>
                    <th>{t('domains.list.operations')}</th>
                  </tr>
                </thead>
                <tbody>
                  {dnsResult.records.map((record, index) => (
                    <tr key={`${record.type}-${record.host}-${index}`}>
                      <td><span className="tag tag-info">{record.type}</span></td>
                      <td><code>{record.host}</code></td>
                      <td><code className="dns-record-value">{record.value}</code></td>
                      <td>
                        <button className="icon-button compact" type="button" title={t('domains.dns.copy')} onClick={() => handleCopy(record.value)}>
                          <Copy size={15} />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </section>

      <section className="section data-section">
        <div className="panel-header">
          <div>
            <h3>{t('domains.list.title')}</h3>
            <div className="panel-caption">{t('domains.list.caption')}</div>
          </div>
        </div>
        <div className="table-wrap">
          <table className="data-table domain-table">
            <thead>
              <tr>
                <th>{t('domains.list.domain')}</th>
                <th>{t('domains.list.binding')}</th>
                <th>{t('domains.list.sync')}</th>
                <th>Postfix</th>
                <th>DKIM</th>
                <th>Selector</th>
                <th>{t('domains.list.mailboxes')}</th>
                <th>{t('domains.list.syncedAt')}</th>
                <th>{t('domains.list.operations')}</th>
              </tr>
            </thead>
            <tbody>
              {bindings.map(binding => {
                const name = binding.domain?.name || `#${binding.domain_id}`
                const mailboxCount = Number(binding.mailbox_count) || 0
                const active = binding.status === 'active'
                return (
                  <tr key={binding.id || `${binding.server_id}-${binding.domain_id}`}>
                    <td>
                      <div className="domain-name-cell">
                        <span className="entity-icon"><Globe2 size={16} /></span>
                        <div>
                          <strong>{name}</strong>
                          {binding.sync_error && <span className="domain-sync-error" title={binding.sync_error}>{binding.sync_error}</span>}
                        </div>
                      </div>
                    </td>
                    <td><StatusTag status={binding.status} /></td>
                    <td><StatusTag status={binding.sync_status} /></td>
                    <td><StatusTag status={binding.postfix_status} /></td>
                    <td><StatusTag status={binding.dkim_status} /></td>
                    <td>{binding.dkim_selector ? <code>{binding.dkim_selector}</code> : '-'}</td>
                    <td><span className={mailboxCount > 0 ? 'tag tag-info' : 'muted-text'}>{mailboxCount}</span></td>
                    <td>{formatDateTime(binding.synced_at)}</td>
                    <td>
                      <div className="row-actions">
                        {active && binding.postfix_status === 'synced' && (
                          <button className="icon-button compact" type="button" title={t('domains.list.createMailbox')} onClick={() => openMailboxCreate(binding)}>
                            <MailPlus size={15} />
                          </button>
                        )}
                        {active && (
                          <button
                            className="icon-button compact danger"
                            type="button"
                            disabled={mailboxCount > 0}
                            title={mailboxCount > 0 ? t('domains.list.removeBlocked', { count: mailboxCount }) : t('domains.list.remove')}
                            onClick={() => askRemove(binding)}
                          >
                            <Trash2 size={15} />
                          </button>
                        )}
                        {!active && <span className="muted-text">{t('domains.list.removed')}</span>}
                      </div>
                    </td>
                  </tr>
                )
              })}
              {bindings.length === 0 && (
                <tr>
                  <td colSpan={9}>
                    <div className="empty-state">
                      <Globe2 size={28} />
                      <strong>{t('domains.empty.title')}</strong>
                      <span>{t('domains.empty.description')}</span>
                    </div>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {confirm && (
        <ConfirmDialog
          {...confirm}
          saving={removing}
          confirmLabel={t('domains.dialogs.removeConfirm')}
          onConfirm={removeDomain}
          onCancel={() => !removing && setConfirm(null)}
        />
      )}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
