import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  ArchiveRestore,
  CheckCircle2,
  Copy,
  Download,
  Inbox,
  KeyRound,
  MailPlus,
  Plus,
  RefreshCw,
  RotateCcw,
  Send,
  Trash2,
  Upload,
  X,
} from 'lucide-react'
import { mailboxAPI, serverAPI, domainAPI, integratedMailboxAPI } from '../api'
import { formatDateTime } from '../i18n'

const STATUS_META = {
  active: { className: 'tag-success' },
  synced: { className: 'tag-success' },
  pending: { className: 'tag-warning' },
  deleting: { className: 'tag-warning' },
  disabled: { className: 'tag-danger' },
  recycled: { className: 'tag-warning' },
  sync_failed: { className: 'tag-danger' },
  soft_deleted: { className: 'tag-warning' },
  purged: { className: 'tag-info' },
  ok: { className: 'tag-success' },
  fail: { className: 'tag-danger' },
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])

  return <div className={`toast toast-${type}`}>{message}</div>
}

function ConfirmDialog({ title, message, confirmLabel, danger = true, onConfirm, onCancel }) {
  const { t } = useTranslation('common')
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal confirm-modal" onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onCancel}>{t('actions.cancel')}</button>
          <button className={`btn ${danger ? 'btn-danger' : 'btn-primary'}`} type="button" onClick={onConfirm}>
            {confirmLabel || t('actions.confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}

function StatusTag({ status }) {
  const { t } = useTranslation('pages')
  const meta = STATUS_META[status] || { className: 'tag-info' }
  return <span className={`tag ${meta.className}`}>{t(`mailboxes.status.${status}`, { defaultValue: status || '-' })}</span>
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

function csvEscape(value) {
  const text = value === undefined || value === null ? '' : String(value)
  if (/[",\n\r]/.test(text)) return `"${text.replace(/"/g, '""')}"`
  return text
}

function downloadCsv(filename, rows) {
  const content = rows.map(row => row.map(csvEscape).join(',')).join('\n')
  const blob = new Blob(['\ufeff' + content], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

function parseMailboxView(value) {
  if (value === 'trash' || value === 'integrated' || value === 'create') return value
  return 'normal'
}

export default function MailboxesPage() {
  const { t } = useTranslation('pages')
  const location = useLocation()
  const initialParams = new URLSearchParams(location.search)
  const initialView = parseMailboxView(initialParams.get('view'))
  const [view, setView] = useState(initialView)
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [servers, setServers] = useState([])
  const [domains, setDomains] = useState([])

  const [search, setSearch] = useState(initialParams.get('search') || '')
  const [domainId, setDomainId] = useState(initialParams.get('domain_id') || '')
  const [serverId, setServerId] = useState(initialParams.get('server_id') || '')
  const [statusFilter, setStatusFilter] = useState(initialParams.get('status') || '')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)

  const [createPrefix, setCreatePrefix] = useState('')
  const [createPassword, setCreatePassword] = useState('')
  const [createServerId, setCreateServerId] = useState('0')
  const [createDomainId, setCreateDomainId] = useState('0')
  const [batchText, setBatchText] = useState('')
  const [createTab, setCreateTab] = useState('single')
  const [creating, setCreating] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [batchResult, setBatchResult] = useState(null)
  const csvInputRef = useRef(null)

  const [pwdModal, setPwdModal] = useState(null)
  const [pwdSaving, setPwdSaving] = useState(false)

  const [integrated, setIntegrated] = useState([])
  const [integratedModal, setIntegratedModal] = useState(null)
  const [integratedSaving, setIntegratedSaving] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const params = { page, size, search, domain_id: domainId, server_id: serverId }
      if (view === 'trash') params.view = 'trash'
      else if (statusFilter) params.status = statusFilter

      const data = await mailboxAPI.list(params)
      setItems(Array.isArray(data?.items) ? data.items : Array.isArray(data) ? data : [])
      setTotal(data?.total_count ?? data?.total ?? 0)
    } catch (e) {
      setToast({ type: 'error', message: t('mailboxes.messages.loadFailed', { message: e.message }) })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [view, search, domainId, serverId, statusFilter, page, size, t])

  useEffect(() => {
    const params = new URLSearchParams(location.search)
    const nextView = parseMailboxView(params.get('view'))
    setView(nextView)
    setSearch(params.get('search') || '')
    setDomainId(params.get('domain_id') || '')
    setServerId(params.get('server_id') || '')
    setStatusFilter(params.get('status') || '')
    setPage(1)
  }, [location.search])

  useEffect(() => {
    if (view === 'normal' || view === 'trash') load()
  }, [load, view])

  useEffect(() => {
    serverAPI.list().then(d => setServers(Array.isArray(d) ? d : [])).catch(() => {})
    domainAPI.list().then(d => setDomains(Array.isArray(d) ? d : [])).catch(() => {})
  }, [])

  const loadIntegrated = useCallback(() => {
    integratedMailboxAPI.list()
      .then(d => setIntegrated(Array.isArray(d) ? d : []))
      .catch(e => setToast({ type: 'error', message: t('mailboxes.messages.integratedLoadFailed', { message: e.message }) }))
  }, [t])

  useEffect(() => {
    if (view === 'integrated') loadIntegrated()
  }, [view, loadIntegrated])

  const switchView = (nextView) => {
    setView(nextView)
    setPage(1)
    if (nextView === 'integrated' && view === 'integrated') loadIntegrated()
    if (nextView === 'create') {
      window.setTimeout(() => {
        document.getElementById('mailbox-create-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }, 0)
    }
  }

  const summary = useMemo(() => ({
    total,
    domains: domainId ? 1 : domains.length,
    servers: serverId ? 1 : servers.length,
    integrated: integrated.length,
  }), [total, domainId, domains.length, serverId, servers.length, integrated.length])

  const serverDomains = (server) => server?.domains || server?.Domains || []
  const selectedCreateServer = servers.find(s => s.id === parseInt(createServerId, 10))
  const selectedCreateDomain = domains.find(d => d.id === parseInt(createDomainId, 10))
  const domainIsServedByServer = (server, domainValue) => {
    if (!server || !domainValue || domainValue === '0') return true
    return serverDomains(server).some(d => d.id === parseInt(domainValue, 10))
  }
  const createServerOptions = createDomainId !== '0'
    ? servers.filter(s => domainIsServedByServer(s, createDomainId))
    : servers
  const createDomainOptions = createServerId !== '0' && selectedCreateServer
    ? serverDomains(selectedCreateServer)
    : domains

  const handleSearch = (e) => {
    e.preventDefault()
    setPage(1)
    load(true)
  }

  const handleCreateServerChange = (value) => {
    setCreateServerId(value)
    const server = servers.find(s => s.id === parseInt(value, 10))
    if (value !== '0' && createDomainId !== '0' && !domainIsServedByServer(server, createDomainId)) {
      setCreateDomainId('0')
      setToast({ type: 'error', message: t('mailboxes.messages.domainFilterCleared') })
    }
  }

  const handleCreateDomainChange = (value) => {
    setCreateDomainId(value)
    if (value !== '0' && createServerId !== '0' && !domainIsServedByServer(selectedCreateServer, value)) {
      setCreateServerId('0')
      setToast({ type: 'error', message: t('mailboxes.messages.serverFilterCleared') })
    }
  }

  const buildCreateItem = (prefix, password = '') => ({
    prefix: prefix.trim(),
    password: password.trim(),
    server_id: parseInt(createServerId, 10) || 0,
    domain_id: parseInt(createDomainId, 10) || 0,
  })

  const submitCreateItems = async (createItems) => {
    const validItems = createItems.filter(item => item.prefix)
    if (validItems.length === 0) return
    setCreating(true)
    setBatchResult(null)
    try {
      const result = await mailboxAPI.batchCreate(validItems)
      setBatchResult(result)
      setToast({ type: 'success', message: t('mailboxes.messages.createDone', { success: result.success || 0, failed: result.failed || 0 }) })
      load(true)
    } catch (e) {
      setToast({ type: 'error', message: t('mailboxes.messages.createFailed', { message: e.message }) })
    } finally {
      setCreating(false)
    }
  }

  const handleSingleCreate = (e) => {
    e.preventDefault()
    setCreateTab('single')
    submitCreateItems([buildCreateItem(createPrefix, createPassword)])
  }

  const handleBatchCreate = (e) => {
    e.preventDefault()
    setCreateTab('batch')
    const createItems = batchText.split('\n').filter(Boolean).map(line => {
      const [prefix, ...rest] = line.split(',')
      return buildCreateItem(prefix, rest.join(','))
    })
    submitCreateItems(createItems)
  }

  const handleCsvUpload = async (event) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    if (!/\.(csv|txt)$/i.test(file.name)) {
      setToast({ type: 'error', message: t('mailboxes.messages.fileType') })
      return
    }
    setCreateTab('upload')
    setUploading(true)
    setBatchResult(null)
    try {
      const result = await mailboxAPI.upload(
        file,
        parseInt(createServerId, 10) || 0,
        parseInt(createDomainId, 10) || 0,
      )
      setBatchResult(result)
      setToast({ type: 'success', message: t('mailboxes.messages.importDone', { success: result.success || 0, failed: result.failed || 0 }) })
      load(true)
    } catch (e) {
      setToast({ type: 'error', message: t('mailboxes.messages.importFailed', { message: e.message }) })
    } finally {
      setUploading(false)
    }
  }

  const downloadCredentialCsv = () => {
    const rows = [
      ['email_address', 'password', 'prefix', 'domain', 'server_id', 'status', 'error'],
      ...(batchResult?.results || []).map(r => [r.email_address || '', r.password || '', r.prefix || '', r.domain || '', r.server_id || '', r.status || '', r.error || '']),
    ]
    const ts = new Date().toISOString().replace(/[-:T]/g, '').slice(0, 12)
    downloadCsv(`mailbox-credentials-${ts}.csv`, rows)
  }

  const copyText = (text) => {
    navigator.clipboard.writeText(text).then(() => {
      setToast({ type: 'success', message: t('mailboxes.messages.copied') })
    })
  }

  const askDelete = (item) => {
    setConfirm({
      title: t('mailboxes.dialogs.deleteTitle'),
      message: t('mailboxes.dialogs.deleteMessage', { email: item.email_address }),
      confirmLabel: t('common:actions.delete'),
      onConfirm: async () => {
        try {
          await mailboxAPI.remove(item.id)
          setToast({ type: 'success', message: t('mailboxes.messages.deleteSubmitted', { email: item.email_address }) })
          load(true)
        } catch (e) {
          setToast({ type: 'error', message: t('mailboxes.messages.deleteFailed', { message: e.message }) })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const askRestore = (item) => {
    setConfirm({
      title: t('mailboxes.dialogs.restoreTitle'),
      message: t('mailboxes.dialogs.restoreMessage', { email: item.email_address }),
      confirmLabel: t('mailboxes.dialogs.restore'),
      danger: false,
      onConfirm: async () => {
        try {
          const data = await mailboxAPI.restore(item.id)
          setToast({ type: 'success', message: data?.password ? t('mailboxes.messages.restoredPassword', { password: data.password }) : t('mailboxes.messages.restored', { email: item.email_address }) })
          load(true)
        } catch (e) {
          setToast({ type: 'error', message: t('mailboxes.messages.restoreFailed', { message: e.message }) })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const askPurge = (item) => {
    setConfirm({
      title: t('mailboxes.dialogs.purgeTitle'),
      message: t('mailboxes.dialogs.purgeMessage', { email: item.email_address }),
      confirmLabel: t('mailboxes.dialogs.purge'),
      onConfirm: async () => {
        try {
          await mailboxAPI.purge(item.id)
          setToast({ type: 'success', message: t('mailboxes.messages.purged', { email: item.email_address }) })
          load(true)
        } catch (e) {
          setToast({ type: 'error', message: t('mailboxes.messages.purgeFailed', { message: e.message }) })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const handlePwdSave = async () => {
    if (!pwdModal.password || pwdModal.password.length < 6) {
      setToast({ type: 'error', message: t('mailboxes.messages.passwordLength') })
      return
    }
    setPwdSaving(true)
    try {
      await mailboxAPI.updatePassword(pwdModal.id, pwdModal.password)
      setPwdModal(null)
      setToast({ type: 'success', message: t('mailboxes.messages.passwordSaved') })
      load(true)
    } catch (e) {
      setToast({ type: 'error', message: t('mailboxes.messages.passwordFailed', { message: e.message }) })
    } finally {
      setPwdSaving(false)
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / size))
  const safePage = Math.min(page, totalPages)

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('mailboxes.title')}</h1>
          <p className="page-subtitle">{t('mailboxes.subtitle')}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => load(true)} disabled={refreshing}>
            {refreshing ? <span className="spinner" /> : <RefreshCw size={16} />}
            {t('common:actions.refresh')}
          </button>
          <button className="btn btn-primary" type="button" onClick={() => switchView('create')}>
            <MailPlus size={16} /> {t('mailboxes.create')}
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <SummaryTile icon={Inbox} label={t('mailboxes.summary.results')} value={summary.total} tone="brand" />
        <SummaryTile icon={CheckCircle2} label={t('mailboxes.summary.domains')} value={summary.domains} tone="success" />
        <SummaryTile icon={Send} label={t('mailboxes.summary.servers')} value={summary.servers} tone="info" />
        <SummaryTile icon={ArchiveRestore} label={t('mailboxes.summary.integrated')} value={summary.integrated} tone="warning" />
      </div>

      <div className="phase-tabs">
        {[
          'normal',
          'trash',
          'integrated',
          'create',
        ].map(value => (
          <button
            key={value}
            className={view === value ? 'active' : ''}
            type="button"
            onClick={() => switchView(value)}
          >
            {t(`mailboxes.tabs.${value}`)}
          </button>
        ))}
      </div>

      {(view === 'normal' || view === 'trash') && (
        <section className="section data-section">
          <div className="panel-header mailbox-toolbar-header">
            <div>
              <h3>{view === 'trash' ? t('mailboxes.list.trashTitle') : t('mailboxes.list.normalTitle')}</h3>
              <div className="panel-caption">
                {view === 'trash' ? t('mailboxes.list.trashCaption') : t('mailboxes.list.normalCaption')}
              </div>
            </div>
          </div>

          <form className="mailbox-toolbar" onSubmit={handleSearch}>
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder={t('mailboxes.list.searchPlaceholder')} />
            <select value={domainId} onChange={e => setDomainId(e.target.value)}>
              <option value="">{t('mailboxes.list.allDomains')}</option>
              {domains.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
            <select value={serverId} onChange={e => setServerId(e.target.value)}>
              <option value="">{t('mailboxes.list.allServers')}</option>
              {servers.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
            {view === 'normal' && (
              <select value={statusFilter} onChange={e => setStatusFilter(e.target.value)}>
                <option value="">{t('mailboxes.list.allStatuses')}</option>
                <option value="active">{t('mailboxes.status.active')}</option>
                <option value="deleting">{t('mailboxes.status.deleting')}</option>
              </select>
            )}
            <button className="btn btn-primary" type="submit">{t('mailboxes.list.filter')}</button>
            <button className="btn btn-outline" type="button" onClick={() => { setSearch(''); setDomainId(''); setServerId(''); setStatusFilter(''); setPage(1) }}>
              <RotateCcw size={15} /> {t('mailboxes.list.reset')}
            </button>
          </form>

          <div className="table-wrap">
            <table className="data-table mailbox-table">
              <thead>
                <tr>
                  <th>{t('mailboxes.list.mailbox')}</th>
                  {view === 'normal' && <th>{t('mailboxes.list.password')}</th>}
                  <th>{t('mailboxes.list.domain')}</th>
                  <th>{t('mailboxes.list.server')}</th>
                  <th>{t('mailboxes.list.status')}</th>
                  {view === 'normal' && <th>{t('mailboxes.list.sync')}</th>}
                  <th>{view === 'trash' ? t('mailboxes.list.deletedAt') : t('mailboxes.list.createdAt')}</th>
                  <th>{t('mailboxes.list.operations')}</th>
                </tr>
              </thead>
              <tbody>
                {items.map(item => (
                  <tr key={item.id}>
                    <td>
                      <div className="entity-cell">
                        <span className="entity-icon"><Inbox size={16} /></span>
                        <div>
                          <strong>{item.email_address}</strong>
                          <span>#{item.id}</span>
                        </div>
                        <button className="icon-button compact" type="button" title={t('mailboxes.list.copyMailbox')} onClick={() => copyText(item.email_address)}>
                          <Copy size={14} />
                        </button>
                      </div>
                    </td>
                    {view === 'normal' && (
                      <td>
                        {item.password ? (
                          <div className="secret-cell">
                            <code>{item.password}</code>
                            <button className="icon-button compact" type="button" title={t('mailboxes.list.copyPassword')} onClick={() => copyText(item.password)}>
                              <Copy size={14} />
                            </button>
                          </div>
                        ) : <span className="muted-text">{t('mailboxes.list.notRecorded')}</span>}
                      </td>
                    )}
                    <td>{item.domain?.name || `#${item.domain_id}`}</td>
                    <td>{item.server?.name || `#${item.server_id}`}</td>
                    <td><StatusTag status={item.status} /></td>
                    {view === 'normal' && <td>{item.sync_status ? <StatusTag status={item.sync_status} /> : <span className="muted-text">-</span>}</td>}
                    <td>{formatDateTime(view === 'trash' ? item.recycled_at : item.created_at)}</td>
                    <td>
                      <div className="row-actions">
                        {view === 'normal' && (
                          <>
                            <button className="icon-button compact" type="button" title={t('mailboxes.list.changePassword')} onClick={() => setPwdModal({ id: item.id, email: item.email_address, password: '' })}>
                              <KeyRound size={15} />
                            </button>
                            {(item.status === 'active' || item.status === 'deleting') && (
                              <button className="icon-button compact danger" type="button" title={t('mailboxes.list.delete')} onClick={() => askDelete(item)}>
                                <Trash2 size={15} />
                              </button>
                            )}
                            {item.status === 'soft_deleted' && (
                              <button className="icon-button compact" type="button" title={t('mailboxes.list.restore')} onClick={() => askRestore(item)}>
                                <ArchiveRestore size={15} />
                              </button>
                            )}
                          </>
                        )}
                        {view === 'trash' && item.status === 'soft_deleted' && (
                          <>
                            <button className="icon-button compact" type="button" title={t('mailboxes.list.restore')} onClick={() => askRestore(item)}>
                              <ArchiveRestore size={15} />
                            </button>
                            <button className="icon-button compact danger" type="button" title={t('mailboxes.list.purge')} onClick={() => askPurge(item)}>
                              <Trash2 size={15} />
                            </button>
                          </>
                        )}
                        {view === 'trash' && item.status === 'purged' && <span className="muted-text">{t('mailboxes.status.purged')}</span>}
                      </div>
                    </td>
                  </tr>
                ))}
                {items.length === 0 && (
                  <tr>
                    <td colSpan={view === 'normal' ? 8 : 6}>
                      <div className="empty-state">
                        {loading ? <span className="spinner" /> : <Inbox size={28} />}
                        <strong>{loading ? t('mailboxes.list.loading') : view === 'trash' ? t('mailboxes.list.trashEmpty') : t('mailboxes.list.empty')}</strong>
                        {!loading && <span>{view === 'trash' ? t('mailboxes.list.trashEmptyDesc') : t('mailboxes.list.emptyDesc')}</span>}
                      </div>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {total > 0 && (
            <div className="pagination-bar">
              <button className="btn btn-sm btn-outline" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>{t('mailboxes.list.previous')}</button>
              <span>{t('mailboxes.list.page', { page: safePage, pages: totalPages, total })}</span>
              <button className="btn btn-sm btn-outline" disabled={safePage >= totalPages} onClick={() => setPage(safePage + 1)}>{t('mailboxes.list.next')}</button>
              <span className="pagination-spacer" />
              <label>
                {t('mailboxes.list.pageSize')}
                <select value={size} onChange={e => { setSize(parseInt(e.target.value, 10)); setPage(1) }}>
                  <option value={20}>20</option>
                  <option value={50}>50</option>
                  <option value={100}>100</option>
                </select>
              </label>
            </div>
          )}
        </section>
      )}

      {view === 'integrated' && (
        <section className="section data-section">
          <div className="panel-header">
            <div>
              <h3>{t('mailboxes.integrated.title')}</h3>
              <div className="panel-caption">{t('mailboxes.integrated.caption')}</div>
            </div>
            <button className="btn btn-primary" type="button" onClick={() => setIntegratedModal({ mode: 'add', data: { email_address: '', display_name: '' } })}>
              <Plus size={16} /> {t('mailboxes.integrated.addTarget')}
            </button>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>{t('mailboxes.integrated.address')}</th><th>{t('mailboxes.integrated.note')}</th><th>{t('mailboxes.integrated.status')}</th><th>{t('mailboxes.integrated.operations')}</th></tr>
              </thead>
              <tbody>
                {integrated.map(m => (
                  <tr key={m.id}>
                    <td><code>{m.email_address}</code></td>
                    <td>{m.display_name || '-'}</td>
                    <td>{m.is_active ? <span className="tag tag-success">{t('mailboxes.integrated.active')}</span> : <span className="muted-text">{t('mailboxes.integrated.standby')}</span>}</td>
                    <td>
                      <div className="row-actions">
                        {!m.is_active && (
                          <button className="btn btn-sm btn-success" type="button" onClick={async () => {
                            try {
                              await integratedMailboxAPI.activate(m.id)
                              setToast({ type: 'success', message: t('mailboxes.integrated.activated') })
                              loadIntegrated()
                            } catch (e) {
                              setToast({ type: 'error', message: e.message })
                            }
                          }}>{t('mailboxes.integrated.activate')}</button>
                        )}
                        <button className="btn btn-sm btn-outline" type="button" onClick={() => setIntegratedModal({ mode: 'edit', data: { ...m } })}>{t('mailboxes.integrated.edit')}</button>
                        <button className="btn btn-sm btn-danger" type="button" disabled={m.is_active} onClick={async () => {
                          setConfirm({
                            title: t('mailboxes.integrated.deleteTitle'),
                            message: t('mailboxes.integrated.deleteMessage', { email: m.email_address }),
                            confirmLabel: t('common:actions.delete'),
                            onConfirm: async () => {
                              try {
                                await integratedMailboxAPI.remove(m.id)
                                setToast({ type: 'success', message: t('mailboxes.messages.deleted') })
                                loadIntegrated()
                              } catch (e) {
                                setToast({ type: 'error', message: e.message })
                              }
                              setConfirm(null)
                            },
                            onCancel: () => setConfirm(null),
                          })
                        }}>{t('common:actions.delete')}</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {integrated.length === 0 && (
                  <tr><td colSpan={4}><div className="empty-state"><Send size={28} /><strong>{t('mailboxes.integrated.empty')}</strong><span>{t('mailboxes.integrated.emptyDesc')}</span></div></td></tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {view === 'create' && (
        <section id="mailbox-create-panel" className="section mailbox-create">
          <div className="panel-header">
            <div>
              <h3>{t('mailboxes.createForm.title')}</h3>
              <div className="panel-caption">{t('mailboxes.createForm.caption')}</div>
            </div>
            {createServerId === '0' && createDomainId === '0' && <span className="tag tag-info">{t('mailboxes.createForm.automatic')}</span>}
          </div>

          <div className="field-grid">
            <div className="form-group">
              <label>{t('mailboxes.createForm.server')}</label>
              <select value={createServerId} onChange={e => handleCreateServerChange(e.target.value)}>
                <option value="0">{t('mailboxes.createForm.autoServer')}</option>
                {createServerOptions.map(s => <option key={s.id} value={s.id}>{s.name} ({t(`servers.status.${s.status}`, { defaultValue: s.status })}, {s.current_load}/{s.capacity})</option>)}
              </select>
              {createDomainId !== '0' && <div className="form-hint">{t('mailboxes.createForm.serverLimited', { name: selectedCreateDomain?.name || `#${createDomainId}` })}</div>}
            </div>
            <div className="form-group">
              <label>{t('mailboxes.createForm.domain')}</label>
              <select value={createDomainId} onChange={e => handleCreateDomainChange(e.target.value)}>
                <option value="0">{t('mailboxes.createForm.autoDomain')}</option>
                {createDomainOptions.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
              {createServerId !== '0' && <div className="form-hint">{t('mailboxes.createForm.domainLimited', { name: selectedCreateServer?.name || `#${createServerId}` })}</div>}
            </div>
          </div>

          <div className="create-grid">
            <form className="create-card" onSubmit={handleSingleCreate}>
              <h4>{t('mailboxes.createForm.single')}</h4>
              <div className="form-group">
                <label>{t('mailboxes.createForm.prefix')}</label>
                <input value={createPrefix} onChange={e => setCreatePrefix(e.target.value)} placeholder="airline-cz-001" required />
              </div>
              <div className="form-group">
                <label>{t('mailboxes.createForm.password')}</label>
                <input value={createPassword} onChange={e => setCreatePassword(e.target.value)} placeholder={t('mailboxes.createForm.passwordPlaceholder')} />
              </div>
              <button className="btn btn-primary" type="submit" disabled={creating || !createPrefix.trim()}>
                {creating && createTab === 'single' && <span className="spinner" />} {t('mailboxes.create')}
              </button>
            </form>

            <form className="create-card" onSubmit={handleBatchCreate}>
              <h4>{t('mailboxes.createForm.batch')}</h4>
              <div className="form-group">
                <label>{t('mailboxes.createForm.list')}</label>
                <textarea value={batchText} onChange={e => setBatchText(e.target.value)} rows={7} placeholder={'airline-cz-001\nairline-cz-002\nairline-cz-003,mypassword123'} />
                <div className="form-hint">{t('mailboxes.createForm.listHint')}</div>
              </div>
              <div className="create-actions">
                <button className="btn btn-primary" type="submit" disabled={creating || uploading || !batchText.trim()}>
                  {creating && createTab === 'batch' && <span className="spinner" />} {t('mailboxes.createForm.batch')}
                </button>
                <input ref={csvInputRef} className="visually-hidden" type="file" accept=".csv,.txt,text/csv,text/plain" onChange={handleCsvUpload} />
                <button className="btn btn-outline" type="button" title={t('mailboxes.createForm.uploadTitle')} disabled={creating || uploading} onClick={() => csvInputRef.current?.click()}>
                  {uploading ? <span className="spinner" /> : <Upload size={16} />} {t('mailboxes.createForm.upload')}
                </button>
                <span className="upload-format-hint">{t('mailboxes.createForm.uploadHint')}</span>
              </div>
            </form>
          </div>

          {batchResult && (
            <div className="batch-result">
              <div className="panel-header">
                <div>
                  <h3>{t('mailboxes.createForm.result')}</h3>
                  <div className="panel-caption">{t('mailboxes.createForm.resultSummary', { success: batchResult.success || 0, failed: batchResult.failed || 0 })}</div>
                </div>
                <div className="row-actions">
                  <button className="btn btn-outline" type="button" onClick={() => switchView('normal')}>
                    {t('mailboxes.createForm.viewAccounts')}
                  </button>
                  <button className="btn btn-outline" type="button" onClick={downloadCredentialCsv}>
                    <Download size={16} /> {t('mailboxes.createForm.downloadCsv')}
                  </button>
                </div>
              </div>
              <div className="result-list">
                {(batchResult.results || []).map((r, i) => (
                  <div className="result-item" key={`${r.email_address || r.prefix}-${i}`}>
                    <div>
                      <strong>{r.email_address || r.prefix}</strong>
                      <span>{r.domain || '-'} {r.server_id ? `#${r.server_id}` : ''}</span>
                    </div>
                    {r.password && <code>{r.password}</code>}
                    <StatusTag status={r.status} />
                    {r.password && <button className="icon-button compact" type="button" title={t('mailboxes.createForm.copyCredential')} onClick={() => copyText(`${r.email_address},${r.password}`)}><Copy size={14} /></button>}
                    {r.error && <span className="result-error">{r.error}</span>}
                  </div>
                ))}
              </div>
            </div>
          )}
        </section>
      )}

      {integratedModal && (
        <div className="drawer-overlay" onClick={() => setIntegratedModal(null)}>
          <aside className="drawer" onClick={e => e.stopPropagation()} aria-label={t('mailboxes.integrated.drawerAria')}>
            <div className="drawer-header">
              <div>
                <div className="drawer-kicker">Forward target</div>
                <h2>{integratedModal.mode === 'add' ? t('mailboxes.integrated.addTitle') : t('mailboxes.integrated.editTitle', { id: integratedModal.data.id })}</h2>
              </div>
              <button className="icon-button" type="button" title={t('common:actions.close')} onClick={() => setIntegratedModal(null)}><X size={18} /></button>
            </div>
            <form className="drawer-body" onSubmit={async (e) => {
              e.preventDefault()
              setIntegratedSaving(true)
              try {
                if (integratedModal.mode === 'add') await integratedMailboxAPI.create(integratedModal.data)
                else await integratedMailboxAPI.update(integratedModal.data.id, integratedModal.data)
                setIntegratedModal(null)
                setToast({ type: 'success', message: t('mailboxes.messages.saved') })
                loadIntegrated()
              } catch (err) {
                setToast({ type: 'error', message: err.message })
              } finally {
                setIntegratedSaving(false)
              }
            }}>
              <div className="form-group">
                <label>{t('mailboxes.integrated.address')}</label>
                <input value={integratedModal.data.email_address} onChange={e => setIntegratedModal({ ...integratedModal, data: { ...integratedModal.data, email_address: e.target.value } })} placeholder="union@example.com" required />
              </div>
              <div className="form-group">
                <label>{t('mailboxes.integrated.note')}</label>
                <input value={integratedModal.data.display_name || ''} onChange={e => setIntegratedModal({ ...integratedModal, data: { ...integratedModal.data, display_name: e.target.value } })} placeholder={t('mailboxes.integrated.notePlaceholder')} />
              </div>
              <div className="drawer-footer">
                <button className="btn btn-outline" type="button" onClick={() => setIntegratedModal(null)}>{t('common:actions.cancel')}</button>
                <button className="btn btn-primary" type="submit" disabled={integratedSaving}>{integratedSaving && <span className="spinner" />} {t('common:actions.save')}</button>
              </div>
            </form>
          </aside>
        </div>
      )}

      {pwdModal && (
        <div className="drawer-overlay" onClick={() => setPwdModal(null)}>
          <aside className="drawer" onClick={e => e.stopPropagation()} aria-label={t('mailboxes.passwordDrawer.aria')}>
            <div className="drawer-header">
              <div>
                <div className="drawer-kicker">Credential</div>
                <h2>{t('mailboxes.passwordDrawer.title')}</h2>
              </div>
              <button className="icon-button" type="button" title={t('common:actions.close')} onClick={() => setPwdModal(null)}><X size={18} /></button>
            </div>
            <div className="drawer-body">
              <div className="form-group">
                <label>{t('mailboxes.passwordDrawer.email')}</label>
                <input value={pwdModal.email} readOnly />
              </div>
              <div className="form-group">
                <label>{t('mailboxes.passwordDrawer.newPassword')}</label>
                <input value={pwdModal.password} onChange={e => setPwdModal({ ...pwdModal, password: e.target.value })} placeholder={t('mailboxes.passwordDrawer.placeholder')} autoFocus />
              </div>
              <div className="drawer-footer">
                <button className="btn btn-outline" type="button" onClick={() => setPwdModal(null)}>{t('common:actions.cancel')}</button>
                <button className="btn btn-primary" type="button" onClick={handlePwdSave} disabled={pwdSaving}>
                  {pwdSaving && <span className="spinner" />} {t('common:actions.save')}
                </button>
              </div>
            </div>
          </aside>
        </div>
      )}

      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
