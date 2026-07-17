import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  CircleOff,
  Database,
  Globe2,
  Settings2,
  Pencil,
  Plus,
  Power,
  RotateCcw,
  Server,
  Trash2,
  X,
} from 'lucide-react'
import { serverAPI } from '../api'
import { configStatusMeta } from '../components/NodeConfigDrawer'
import { formatDateTime } from '../i18n'

const STATUS_META = {
  healthy: { className: 'status-healthy', icon: CheckCircle2 },
  degraded: { className: 'status-degraded', icon: AlertTriangle },
  draining: { className: 'status-draining', icon: Activity },
  down: { className: 'status-down', icon: CircleOff },
}

const EMPTY_FORM = {
  name: '',
  api_host: '',
  capacity: 5000,
  heartbeat_interval: 30,
  status: 'healthy',
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

function StatusBadge({ status }) {
  const { t } = useTranslation('pages')
  const meta = STATUS_META[status] || STATUS_META.down
  const Icon = meta.icon
  return (
    <span className={`status-badge ${meta.className}`}>
      <Icon size={13} />
      {t(`servers.status.${status}`, { defaultValue: t('servers.status.down') })}
    </span>
  )
}

function clampLoad(current, capacity) {
  const total = Number(capacity) || 0
  if (total <= 0) return 0
  return Math.max(0, Math.min(100, Math.round(((Number(current) || 0) / total) * 100)))
}

function loadColor(percent, status) {
  if (status === 'down') return 'var(--color-danger)'
  if (status === 'degraded' || percent >= 80) return 'var(--color-warning)'
  if (status === 'draining') return 'var(--color-info)'
  return 'var(--color-success)'
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

function ServerDrawer({ mode, form, saving, onChange, onSave, onClose, onDelete }) {
  const { t } = useTranslation('pages')
  const isEdit = mode === 'edit'

  const updateField = (field, value) => {
    onChange(prev => ({ ...prev, [field]: value }))
  }

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer" onClick={e => e.stopPropagation()} aria-label={isEdit ? t('servers.drawer.editAria') : t('servers.drawer.registerAria')}>
        <div className="drawer-header">
          <div>
            <div className="drawer-kicker">Mail node</div>
            <h2>{isEdit ? t('servers.drawer.editTitle', { name: form.name || `#${form.id}` }) : t('servers.drawer.registerTitle')}</h2>
          </div>
          <button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}>
            <X size={18} />
          </button>
        </div>

        <form className="drawer-body" onSubmit={onSave}>
          <div className="form-group">
            <label>{t('servers.drawer.name')}</label>
            <input
              value={form.name}
              onChange={e => updateField('name', e.target.value)}
              placeholder={t('servers.drawer.namePlaceholder')}
              required
            />
          </div>
          <div className="form-group">
            <label>{t('servers.drawer.apiHost')}</label>
            <input
              value={form.api_host}
              onChange={e => updateField('api_host', e.target.value)}
              placeholder={t('servers.drawer.apiPlaceholder')}
              required
            />
            <div className="form-hint">{t('servers.drawer.apiHint')}</div>
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>{t('servers.drawer.capacity')}</label>
              <input
                type="number"
                min={1}
                value={form.capacity}
                onChange={e => updateField('capacity', parseInt(e.target.value, 10) || 0)}
              />
            </div>
            <div className="form-group">
              <label>{t('servers.drawer.heartbeat')}</label>
              <input
                type="number"
                min={5}
                max={600}
                value={form.heartbeat_interval}
                onChange={e => updateField('heartbeat_interval', parseInt(e.target.value, 10) || 0)}
              />
              <div className="form-hint">{t('servers.drawer.heartbeatHint')}</div>
            </div>
          </div>
          {isEdit && (
            <div className="form-group">
              <label>{t('servers.drawer.status')}</label>
              <select value={form.status} onChange={e => updateField('status', e.target.value)}>
                {Object.keys(STATUS_META).map(status => <option key={status} value={status}>{t(`servers.status.${status}`)}</option>)}
              </select>
            </div>
          )}

          <div className="drawer-footer">
            {isEdit && (
              <button className="btn btn-outline btn-danger-outline" type="button" onClick={onDelete}>
                <Trash2 size={16} /> {t('common:actions.delete')}
              </button>
            )}
            <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving && <span className="spinner" />}
              {isEdit ? t('servers.drawer.saveChanges') : t('servers.register')}
            </button>
          </div>
        </form>
      </aside>
    </div>
  )
}

export default function ServersPage() {
  const { t } = useTranslation('pages')
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const searchQuery = (searchParams.get('search') || '').trim()
  const [servers, setServers] = useState([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [drawerMode, setDrawerMode] = useState(null)
  const [form, setForm] = useState(EMPTY_FORM)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const data = await serverAPI.list()
      setServers(Array.isArray(data) ? data : [])
    } catch (e) {
      setToast({ type: 'error', message: t('servers.messages.loadFailed', { message: e.message }) })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const visibleServers = useMemo(() => {
    const needle = searchQuery.toLowerCase()
    if (!needle) return servers
    return servers.filter(server => {
      const domainText = (server.domains || []).map(domain => domain.name).join(' ')
      return [
        server.id,
        server.name,
        server.api_host,
        server.status,
        domainText,
      ].some(value => String(value || '').toLowerCase().includes(needle))
    })
  }, [searchQuery, servers])

  const summary = useMemo(() => {
    const byStatus = visibleServers.reduce((acc, server) => {
      acc[server.status || 'down'] = (acc[server.status || 'down'] || 0) + 1
      return acc
    }, {})
    return {
      total: visibleServers.length,
      healthy: byStatus.healthy || 0,
      degraded: byStatus.degraded || 0,
      draining: byStatus.draining || 0,
      down: byStatus.down || 0,
    }
  }, [visibleServers])

  const openCreate = () => {
    setForm(EMPTY_FORM)
    setDrawerMode('create')
  }

  const openEdit = (server) => {
    setForm({
      id: server.id,
      name: server.name || '',
      api_host: server.api_host || '',
      capacity: server.capacity || 5000,
      heartbeat_interval: server.heartbeat_interval || 30,
      status: server.status || 'healthy',
    })
    setDrawerMode('edit')
  }

  const closeDrawer = () => {
    setDrawerMode(null)
    setForm(EMPTY_FORM)
  }

  const handleSave = async (e) => {
    e.preventDefault()
    setSaving(true)
    const payload = {
      name: form.name,
      api_host: form.api_host,
      capacity: Number(form.capacity) || 5000,
      heartbeat_interval: Number(form.heartbeat_interval) || 30,
      status: form.status,
    }
    try {
      if (drawerMode === 'edit') {
        await serverAPI.update(form.id, payload)
        setToast({ type: 'success', message: t('servers.messages.saved') })
      } else {
        await serverAPI.create({
          name: payload.name,
          api_host: payload.api_host,
          capacity: payload.capacity,
        })
        setToast({ type: 'success', message: t('servers.messages.registered') })
      }
      closeDrawer()
      load(true)
    } catch (err) {
      setToast({ type: 'error', message: err.message })
    } finally {
      setSaving(false)
    }
  }

  const toggleStatus = (server) => {
    const newStatus = server.status === 'draining' ? 'healthy' : 'draining'
    const action = newStatus === 'draining' ? t('servers.dialogs.drain') : t('servers.dialogs.resume')
    setConfirm({
      title: t('servers.dialogs.statusTitle', { action }),
      message: t('servers.dialogs.statusMessage', { name: server.name, action }),
      confirmLabel: action,
      danger: newStatus === 'draining',
      onConfirm: async () => {
        try {
          await serverAPI.update(server.id, { status: newStatus })
          setToast({ type: 'success', message: t('servers.messages.statusUpdated') })
          load(true)
        } catch (err) {
          setToast({ type: 'error', message: err.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const askDelete = (server = form) => {
    setConfirm({
      title: t('servers.dialogs.deleteTitle'),
      message: t('servers.dialogs.deleteMessage', { name: server.name }),
      confirmLabel: t('common:actions.delete'),
      onConfirm: async () => {
        try {
          await serverAPI.remove(server.id)
          setToast({ type: 'success', message: t('servers.messages.deleted') })
          closeDrawer()
          load(true)
        } catch (err) {
          setToast({ type: 'error', message: err.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> {t('servers.loading')}
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('servers.title')}</h1>
          <p className="page-subtitle">{t('servers.subtitle')}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => load(true)} disabled={refreshing}>
            {refreshing ? <span className="spinner" /> : <RotateCcw size={16} />}
            {t('common:actions.refresh')}
          </button>
          <button className="btn btn-primary" type="button" onClick={openCreate}>
            <Plus size={16} /> {t('servers.register')}
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <SummaryTile icon={Server} label={t('servers.summary.total')} value={summary.total} tone="brand" />
        <SummaryTile icon={CheckCircle2} label={t('servers.summary.healthy')} value={summary.healthy} tone="success" />
        <SummaryTile icon={AlertTriangle} label={t('servers.summary.degraded')} value={summary.degraded} tone="warning" />
        <SummaryTile icon={Activity} label={t('servers.summary.draining')} value={summary.draining} tone="info" />
        <SummaryTile icon={CircleOff} label={t('servers.summary.down')} value={summary.down} tone="danger" />
      </div>

      <section className="section data-section">
        <div className="panel-header">
          <div>
            <h3>{t('servers.list.title')}</h3>
            <div className="panel-caption">
              {searchQuery ? t('servers.list.searchCaption', { query: searchQuery, count: visibleServers.length }) : t('servers.list.caption')}
            </div>
          </div>
          {searchQuery && (
            <button className="btn btn-sm btn-outline" type="button" onClick={() => setSearchParams({})}>
              {t('common:actions.clearSearch')}
            </button>
          )}
        </div>
        <div className="table-wrap">
          <table className="data-table server-table">
            <thead>
              <tr>
                <th>{t('servers.list.node')}</th>
                <th>API</th>
                <th>{t('servers.list.domains')}</th>
                <th>{t('servers.list.load')}</th>
                <th>{t('servers.list.status')}</th>
                <th>{t('servers.list.heartbeat')}</th>
                <th>{t('servers.list.probe')}</th>
                <th>{t('servers.list.failures')}</th>
                <th>{t('servers.list.config')}</th>
                <th>{t('servers.list.operations')}</th>
              </tr>
            </thead>
            <tbody>
              {visibleServers.map(server => {
                const percent = clampLoad(server.current_load, server.capacity)
                return (
                  <tr key={server.id}>
                    <td>
                      <div className="entity-cell">
                        <span className="entity-icon"><Server size={17} /></span>
                        <div>
                          <strong>{server.name || `server-${server.id}`}</strong>
                          <span>#{server.id}</span>
                        </div>
                      </div>
                    </td>
                    <td><code>{server.api_host || '-'}</code></td>
                    <td>
                      <div className="tag-list">
                        {server.domains && server.domains.length > 0
                          ? server.domains.map(domain => <span key={domain.id || domain.name} className="tag tag-info">{domain.name}</span>)
                          : <span className="muted-text">{t('servers.list.unbound')}</span>}
                      </div>
                    </td>
                    <td>
                      <div className="load-cell">
                        <div className="load-meta">
                          <span>{server.current_load || 0} / {server.capacity || 0}</span>
                          <span>{percent}%</span>
                        </div>
                        <div className="progress" aria-label={t('servers.list.loadAria', { name: server.name, percent })}>
                          <div
                            className="progress-bar"
                            style={{
                              '--progress': `${percent}%`,
                              '--progress-color': loadColor(percent, server.status),
                            }}
                          />
                        </div>
                      </div>
                    </td>
                    <td><StatusBadge status={server.status} /></td>
                    <td>{formatDateTime(server.last_heartbeat)}</td>
                    <td>{formatDateTime(server.last_probe_at)}</td>
                    <td>
                      <span className={(server.probe_fail_count || 0) > 0 ? 'tag tag-warning' : 'tag tag-success'}>
                        {server.probe_fail_count || 0}
                      </span>
                    </td>
                    <td>
                      {server.config_summary
						? <div className={`config-summary ${server.config_summary.status}`}><strong>{server.config_summary.effective_value ? t('servers.list.configHours', { value: server.config_summary.effective_value }) : t('common:states.notReported')}</strong><span>{configStatusMeta(server.config_summary.status, t).label}</span></div>
                        : <span className="muted-text">{t('common:states.notReported')}</span>}
                    </td>
                    <td>
                      <div className="row-actions">
                        <button className="icon-button compact" type="button" title={t('servers.list.domainPool')} onClick={() => navigate(`/servers/${server.id}/domains`)}><Globe2 size={15} /></button>
                        <button className="icon-button compact" type="button" title={t('servers.list.config')} onClick={() => navigate(`/config?server_id=${server.id}`)}><Settings2 size={15} /></button>
                        <button className="icon-button compact" type="button" title={t('common:actions.edit')} onClick={() => openEdit(server)}>
                          <Pencil size={15} />
                        </button>
                        <button className="icon-button compact" type="button" title={server.status === 'draining' ? t('servers.dialogs.resume') : t('servers.dialogs.drain')} onClick={() => toggleStatus(server)}>
                          {server.status === 'draining' ? <Power size={15} /> : <Activity size={15} />}
                        </button>
                        <button className="icon-button compact danger" type="button" title={t('common:actions.delete')} onClick={() => askDelete(server)}>
                          <Trash2 size={15} />
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
              {visibleServers.length === 0 && (
                <tr>
                  <td colSpan={10}>
                    <div className="empty-state">
                      <Database size={28} />
                      <strong>{searchQuery ? t('servers.list.emptySearch') : t('servers.list.empty')}</strong>
                      <span>{searchQuery ? t('servers.list.emptySearchDesc') : t('servers.list.emptyDesc')}</span>
                      {searchQuery ? (
                        <button className="btn btn-outline" type="button" onClick={() => setSearchParams({})}>{t('common:actions.clearSearch')}</button>
                      ) : (
                        <button className="btn btn-primary" type="button" onClick={openCreate}>
                          <Plus size={16} /> {t('servers.register')}
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {drawerMode && (
        <ServerDrawer
          mode={drawerMode}
          form={form}
          saving={saving}
          onChange={setForm}
          onSave={handleSave}
          onClose={closeDrawer}
          onDelete={() => askDelete(form)}
        />
      )}
      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
