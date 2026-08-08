import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  CircleOff,
  Clipboard,
  Database,
  Globe2,
  KeyRound,
  Settings2,
  Pencil,
  Plus,
  Power,
  RotateCcw,
  Server,
  ShieldCheck,
  Trash2,
  Unplug,
  UserCheck,
  UserX,
  X,
} from 'lucide-react'
import { nodeEnrollmentAPI, serverAPI } from '../api'
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
  smtp_host: '',
  imap_host: '',
  public_host: '',
  mail_public_ips: '',
  capacity: 5000,
  heartbeat_interval: 30,
  status: 'healthy',
}

const EMPTY_INVITATION_FORM = {
  name: '',
  environment: 'production',
  region: '',
  labels: '',
  expected_node_uuid: '',
  recovery_server_id: 0,
  expires_in_minutes: 30,
  max_uses: 1,
  auto_approve: false,
}

const REQUEST_STATE_TONE = {
  pending: 'warning', approved: 'info', rejected: 'danger', completed: 'success', expired: 'danger',
}

const INVITATION_STATE_TONE = {
  active: 'success', used: 'info', expired: 'danger', revoked: 'danger',
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

function SecretDialog({ title, alert, secret, copyLabel, onCopy, onClose }) {
  const { t } = useTranslation('common')
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal credential-modal" onClick={event => event.stopPropagation()}>
        <div className="enrollment-modal-heading">
          <span className="module-icon"><KeyRound size={18} /></span>
          <h3>{title}</h3>
          <button className="icon-button" type="button" title={t('actions.close')} onClick={onClose}><X size={18} /></button>
        </div>
        <div className="inline-message token-alert">{alert}</div>
        <div className="token-secret"><code>{secret}</code></div>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onClose}>{t('actions.close')}</button>
          <button className="btn btn-primary" type="button" onClick={onCopy}><Clipboard size={16} /> {copyLabel}</button>
        </div>
      </div>
    </div>
  )
}

function InvitationDrawer({ form, servers, saving, onChange, onSubmit, onClose }) {
  const { t } = useTranslation('pages')
  const update = (field, value) => onChange(current => ({ ...current, [field]: value }))
  const selectedServer = servers.find(server => server.id === Number(form.recovery_server_id))
  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer" onClick={event => event.stopPropagation()} aria-label={t('servers.enrollment.invitationAria')}>
        <div className="drawer-header">
          <div><div className="drawer-kicker">{t('servers.enrollment.kicker')}</div><h2>{t('servers.enrollment.createTitle')}</h2></div>
          <button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button>
        </div>
        <form className="drawer-body" onSubmit={onSubmit}>
          <div className="form-group"><label>{t('servers.enrollment.name')}</label><input required value={form.name} onChange={event => update('name', event.target.value)} placeholder={t('servers.enrollment.namePlaceholder')} /></div>
          <div className="field-grid">
            <div className="form-group"><label>{t('servers.enrollment.environment')}</label><input value={form.environment} onChange={event => update('environment', event.target.value)} /></div>
            <div className="form-group"><label>{t('servers.enrollment.region')}</label><input value={form.region} onChange={event => update('region', event.target.value)} placeholder={t('servers.enrollment.regionPlaceholder')} /></div>
          </div>
          <div className="form-group">
            <label>{t('servers.enrollment.recoveryServer')}</label>
            <select value={form.recovery_server_id} onChange={event => update('recovery_server_id', Number(event.target.value))}>
              <option value={0}>{t('servers.enrollment.newNode')}</option>
              {servers.map(server => <option key={server.id} value={server.id}>#{server.id} · {server.name} · {server.node_uuid || `${t('servers.enrollment.legacyMigration')} · ${server.api_host}`}</option>)}
            </select>
            {selectedServer && !selectedServer.node_uuid && <div className="form-hint">{t('servers.enrollment.migrationHint', { id: selectedServer.id, host: selectedServer.api_host })}</div>}
          </div>
          {!form.recovery_server_id && <div className="form-group"><label>{t('servers.enrollment.expectedUUID')}</label><input value={form.expected_node_uuid} onChange={event => update('expected_node_uuid', event.target.value)} placeholder={t('servers.enrollment.expectedUUIDPlaceholder')} /><div className="form-hint">{t('servers.enrollment.expectedUUIDHint')}</div></div>}
          <div className="form-group"><label>{t('servers.enrollment.labels')}</label><textarea rows={3} value={form.labels} onChange={event => update('labels', event.target.value)} placeholder={t('servers.enrollment.labelsPlaceholder')} /></div>
          <div className="field-grid">
            <div className="form-group"><label>{t('servers.enrollment.expires')}</label><input type="number" min={1} max={1440} value={form.expires_in_minutes} onChange={event => update('expires_in_minutes', Number(event.target.value))} /></div>
            <div className="form-group"><label>{t('servers.enrollment.maxUses')}</label><input type="number" min={1} max={100} disabled={Boolean(form.recovery_server_id)} value={form.max_uses} onChange={event => update('max_uses', Number(event.target.value))} /></div>
          </div>
          <label className="enrollment-toggle"><span><strong>{t('servers.enrollment.autoApprove')}</strong><small>{t('servers.enrollment.autoApproveHint')}</small></span><span className="toggle"><input type="checkbox" checked={form.auto_approve} disabled={Boolean(form.recovery_server_id)} onChange={event => update('auto_approve', event.target.checked)} /><span className="toggle-slider" /></span></label>
          <div className="drawer-footer"><button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button><button className="btn btn-primary" type="submit" disabled={saving}>{saving && <span className="spinner" />}<ShieldCheck size={16} /> {t('servers.enrollment.create')}</button></div>
        </form>
      </aside>
    </div>
  )
}

function RequestDialog({ details, busy, onApprove, onReject, onClose }) {
  const { t } = useTranslation('pages')
  const request = details.request
  const invitation = details.invitation
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal enrollment-request-modal" onClick={event => event.stopPropagation()}>
        <div className="enrollment-modal-heading"><span className="module-icon"><ShieldCheck size={18} /></span><div><h3>{request.requested_name}</h3><code>{request.id}</code></div><button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button></div>
        <dl className="enrollment-details">
          <div><dt>{t('servers.enrollment.nodeUUID')}</dt><dd><code>{request.requested_node_uuid}</code></dd></div>
          <div><dt>{t('servers.enrollment.machine')}</dt><dd>{request.hostname || '-'} · {request.os || '-'} / {request.arch || '-'}</dd></div>
          <div><dt>{t('servers.enrollment.fingerprint')}</dt><dd><code>{request.machine_fingerprint || '-'}</code></dd></div>
          <div><dt>{t('servers.enrollment.agent')}</dt><dd>{request.agent_version || '-'}</dd></div>
          <div><dt>{t('servers.enrollment.source')}</dt><dd>{request.source_ip || '-'}</dd></div>
          <div><dt>{t('servers.enrollment.invitation')}</dt><dd>{invitation.name} · <code>{invitation.token_prefix}</code></dd></div>
          {invitation.purpose === 'migration' && details.target_server && <div><dt>{t('servers.enrollment.migrationTarget')}</dt><dd><strong>{details.target_server.name}</strong> · #{details.target_server.id} · <code>{details.target_server.api_host}</code></dd></div>}
          <div><dt>{t('servers.enrollment.createdBy')}</dt><dd>{invitation.created_by || '-'}</dd></div>
          <div><dt>{t('servers.enrollment.requestedAt')}</dt><dd>{formatDateTime(request.created_at)}</dd></div>
          {request.review_note && <div><dt>{t('servers.enrollment.reviewNote')}</dt><dd>{request.review_note}</dd></div>}
        </dl>
        <div className="modal-footer">
          {request.state === 'pending' && <><button className="btn btn-danger" type="button" disabled={busy} onClick={onReject}><UserX size={16} /> {t('servers.enrollment.reject')}</button><button className="btn btn-primary" type="button" disabled={busy} onClick={onApprove}><UserCheck size={16} /> {t('servers.enrollment.approve')}</button></>}
          {request.state !== 'pending' && <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.close')}</button>}
        </div>
      </div>
    </div>
  )
}

function CredentialDialog({ server, credentials, busy, onRotate, onRevoke, onClose }) {
  const { t } = useTranslation('pages')
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal enrollment-request-modal" onClick={event => event.stopPropagation()}>
        <div className="enrollment-modal-heading"><span className="module-icon"><KeyRound size={18} /></span><div><h3>{t('servers.credentials.title', { name: server.name })}</h3><code>{server.node_uuid}</code></div><button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button></div>
        <div className="table-wrap"><table className="data-table compact-table"><thead><tr><th>{t('servers.credentials.version')}</th><th>{t('servers.credentials.prefix')}</th><th>{t('servers.credentials.state')}</th><th>{t('servers.credentials.lastUsed')}</th></tr></thead><tbody>{credentials.map(item => <tr key={item.id}><td>v{item.version}</td><td><code>{item.credential_prefix}</code></td><td><span className={`tag tag-${item.state === 'active' ? 'success' : item.state === 'rotating' ? 'warning' : 'danger'}`}>{t(`servers.credentials.states.${item.state}`)}</span></td><td>{formatDateTime(item.last_used_at)}</td></tr>)}{credentials.length === 0 && <tr><td colSpan={4} className="muted-text">{t('servers.credentials.empty')}</td></tr>}</tbody></table></div>
        <div className="modal-footer"><button className="btn btn-danger" type="button" disabled={busy || credentials.every(item => item.state === 'revoked')} onClick={onRevoke}><UserX size={16} /> {t('servers.credentials.revoke')}</button><button className="btn btn-primary" type="button" disabled={busy} onClick={onRotate}><RotateCcw size={16} /> {t('servers.credentials.rotate')}</button></div>
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
              <label>{t('servers.drawer.smtpHost')}</label>
              <input
                value={form.smtp_host}
                onChange={e => updateField('smtp_host', e.target.value)}
                placeholder={t('servers.drawer.smtpPlaceholder')}
              />
            </div>
            <div className="form-group">
              <label>{t('servers.drawer.imapHost')}</label>
              <input
                value={form.imap_host}
                onChange={e => updateField('imap_host', e.target.value)}
                placeholder={t('servers.drawer.imapPlaceholder')}
              />
            </div>
            <div className="form-hint field-grid-hint">{t('servers.drawer.mailHostHint')}</div>
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>{t('servers.drawer.publicHost')}</label>
              <input
                value={form.public_host}
                onChange={e => updateField('public_host', e.target.value)}
                placeholder={t('servers.drawer.publicHostPlaceholder')}
              />
            </div>
            <div className="form-group">
              <label>{t('servers.drawer.publicIPs')}</label>
              <textarea
                rows={3}
                value={form.mail_public_ips}
                onChange={e => updateField('mail_public_ips', e.target.value)}
                placeholder={t('servers.drawer.publicIPsPlaceholder')}
              />
            </div>
            <div className="form-hint field-grid-hint">{t('servers.drawer.publicAddressHint')}</div>
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
  const [invitations, setInvitations] = useState([])
  const [enrollmentRequests, setEnrollmentRequests] = useState([])
  const [invitationForm, setInvitationForm] = useState(EMPTY_INVITATION_FORM)
  const [invitationDrawer, setInvitationDrawer] = useState(false)
  const [enrollmentBusy, setEnrollmentBusy] = useState(false)
  const [secretDialog, setSecretDialog] = useState(null)
  const [requestDetails, setRequestDetails] = useState(null)
  const [credentialDialog, setCredentialDialog] = useState(null)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const [serverData, invitationData, requestData] = await Promise.all([
        serverAPI.list(), nodeEnrollmentAPI.invitations(), nodeEnrollmentAPI.requests(),
      ])
      setServers(Array.isArray(serverData) ? serverData : [])
      setInvitations(Array.isArray(invitationData) ? invitationData : [])
      setEnrollmentRequests(Array.isArray(requestData) ? requestData : [])
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
        server.public_host,
        ...(server.mail_public_ips || []),
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
      smtp_host: server.smtp_host || '',
      imap_host: server.imap_host || '',
      public_host: server.public_host || '',
      mail_public_ips: (server.mail_public_ips || []).join('\n'),
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

  const createInvitation = async (event) => {
    event.preventDefault()
    setEnrollmentBusy(true)
    const labels = invitationForm.labels.split('\n').reduce((values, line) => {
      const separator = line.indexOf('=')
      if (separator > 0) values[line.slice(0, separator).trim()] = line.slice(separator + 1).trim()
      return values
    }, {})
    try {
      const result = await nodeEnrollmentAPI.createInvitation({
        ...invitationForm,
        labels,
        recovery_server_id: Number(invitationForm.recovery_server_id) || 0,
        max_uses: invitationForm.recovery_server_id ? 1 : Number(invitationForm.max_uses) || 1,
        auto_approve: invitationForm.recovery_server_id ? false : invitationForm.auto_approve,
      })
      setInvitationDrawer(false)
      setInvitationForm(EMPTY_INVITATION_FORM)
      setSecretDialog({
        title: t('servers.enrollment.tokenTitle'), alert: t('servers.enrollment.tokenAlert'),
        secret: result.token, copyLabel: t('servers.enrollment.copyToken'),
      })
      load(true)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setEnrollmentBusy(false)
    }
  }

  const copySecret = async () => {
    try {
      await navigator.clipboard.writeText(secretDialog.secret)
      setToast({ type: 'success', message: t('servers.enrollment.copied') })
    } catch {
      setToast({ type: 'error', message: t('servers.enrollment.copyFailed') })
    }
  }

  const revokeInvitation = (invitation) => setConfirm({
    title: t('servers.enrollment.revokeTitle'),
    message: t('servers.enrollment.revokeMessage', { name: invitation.name }),
    confirmLabel: t('servers.enrollment.revoke'),
    onConfirm: async () => {
      try {
        await nodeEnrollmentAPI.revokeInvitation(invitation.id)
        setToast({ type: 'success', message: t('servers.enrollment.revoked') })
        load(true)
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      }
      setConfirm(null)
    },
    onCancel: () => setConfirm(null),
  })

  const deleteInvitation = (invitation) => setConfirm({
    title: t('servers.enrollment.deleteTitle'),
    message: t('servers.enrollment.deleteMessage', { name: invitation.name }),
    confirmLabel: t('servers.enrollment.delete'),
    onConfirm: async () => {
      try {
        await nodeEnrollmentAPI.deleteInvitation(invitation.id)
        setToast({ type: 'success', message: t('servers.enrollment.deleted') })
        load(true)
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      }
      setConfirm(null)
    },
    onCancel: () => setConfirm(null),
  })

  const openRequest = async (requestID) => {
    setEnrollmentBusy(true)
    try {
      setRequestDetails(await nodeEnrollmentAPI.request(requestID))
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setEnrollmentBusy(false)
    }
  }

  const reviewRequest = async (action) => {
    setEnrollmentBusy(true)
    try {
      if (action === 'approve') await nodeEnrollmentAPI.approve(requestDetails.request.id)
      else await nodeEnrollmentAPI.reject(requestDetails.request.id)
      setToast({ type: 'success', message: t(action === 'approve' ? 'servers.enrollment.approved' : 'servers.enrollment.rejected') })
      setRequestDetails(null)
      load(true)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setEnrollmentBusy(false)
    }
  }

  const openCredentials = async (server) => {
    setEnrollmentBusy(true)
    try {
      const credentials = await nodeEnrollmentAPI.credentials(server.id)
      setCredentialDialog({ server, credentials: Array.isArray(credentials) ? credentials : [] })
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setEnrollmentBusy(false)
    }
  }

  const rotateCredential = async () => {
    setEnrollmentBusy(true)
    try {
      const result = await nodeEnrollmentAPI.rotateCredential(credentialDialog.server.id)
      setCredentialDialog(null)
      setSecretDialog({
        title: t('servers.credentials.tokenTitle'), alert: t('servers.credentials.tokenAlert'),
        secret: result.credential, copyLabel: t('servers.credentials.copyToken'),
      })
      load(true)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setEnrollmentBusy(false)
    }
  }

  const revokeCredentials = () => setConfirm({
    title: t('servers.credentials.revokeTitle'),
    message: t('servers.credentials.revokeMessage', { name: credentialDialog.server.name }),
    confirmLabel: t('servers.credentials.revoke'),
    onConfirm: async () => {
      setEnrollmentBusy(true)
      try {
        await nodeEnrollmentAPI.revokeCredentials(credentialDialog.server.id)
        setCredentialDialog(null)
        setToast({ type: 'success', message: t('servers.credentials.revoked') })
        load(true)
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      } finally {
        setEnrollmentBusy(false)
        setConfirm(null)
      }
    },
    onCancel: () => setConfirm(null),
  })

  const disconnectNode = (server) => setConfirm({
    title: t('servers.connection.disconnectTitle'),
    message: t('servers.connection.disconnectMessage', { name: server.name }),
    confirmLabel: t('servers.connection.disconnect'),
    onConfirm: async () => {
      try {
        await nodeEnrollmentAPI.disconnect(server.id)
        setToast({ type: 'success', message: t('servers.connection.disconnected') })
        load(true)
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      }
      setConfirm(null)
    },
    onCancel: () => setConfirm(null),
  })

  const handleSave = async (e) => {
    e.preventDefault()
    setSaving(true)
    const payload = {
      name: form.name,
      api_host: form.api_host,
      smtp_host: form.smtp_host,
      imap_host: form.imap_host,
      public_host: form.public_host,
      mail_public_ips: form.mail_public_ips.split(/[\s,]+/).map(value => value.trim()).filter(Boolean),
      capacity: Number(form.capacity) || 5000,
      heartbeat_interval: Number(form.heartbeat_interval) || 30,
      status: form.status,
    }
    try {
      if (drawerMode === 'edit') {
        await serverAPI.update(form.id, payload)
        setToast({ type: 'success', message: t('servers.messages.saved') })
      } else {
        await serverAPI.create(payload)
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
          <button className="btn btn-primary" type="button" onClick={() => setInvitationDrawer(true)}>
            <ShieldCheck size={16} /> {t('servers.enrollment.create')}
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

      <div className="enrollment-workspace">
        <section className="section data-section">
          <div className="panel-header"><div><h3>{t('servers.enrollment.invitationsTitle')}</h3><div className="panel-caption">{t('servers.enrollment.invitationsCaption')}</div></div><span className="tag tag-info">{invitations.length}</span></div>
          <div className="table-wrap"><table className="data-table compact-table"><thead><tr><th>{t('servers.enrollment.invitation')}</th><th>{t('servers.enrollment.scope')}</th><th>{t('servers.enrollment.usage')}</th><th>{t('servers.enrollment.expiresAt')}</th><th>{t('servers.credentials.state')}</th><th>{t('servers.list.operations')}</th></tr></thead><tbody>
            {invitations.map(invitation => <tr key={invitation.id}>
              <td><strong>{invitation.name}</strong><div><code>{invitation.token_prefix}</code></div></td>
              <td>{invitation.purpose === 'recovery' ? t('servers.enrollment.recovery') : invitation.purpose === 'migration' ? t('servers.enrollment.migration') : invitation.expected_node_uuid ? t('servers.enrollment.prebound') : t('servers.enrollment.standard')}<div className="muted-text">{[invitation.environment, invitation.region].filter(Boolean).join(' · ') || '-'}</div></td>
              <td>{invitation.used_count} / {invitation.max_uses}</td><td>{formatDateTime(invitation.expires_at)}</td>
              <td><span className={`tag tag-${INVITATION_STATE_TONE[invitation.state] || 'info'}`}>{t(`servers.enrollment.invitationState.${invitation.state}`)}</span></td>
              <td>
                {invitation.state === 'revoked'
                  ? <button className="icon-button compact danger" type="button" title={t('servers.enrollment.delete')} onClick={() => deleteInvitation(invitation)}><Trash2 size={15} /></button>
                  : <button className="icon-button compact danger" type="button" disabled={!['active', 'used'].includes(invitation.state)} title={t('servers.enrollment.revoke')} onClick={() => revokeInvitation(invitation)}><UserX size={15} /></button>}
              </td>
            </tr>)}
            {invitations.length === 0 && <tr><td colSpan={6}><div className="enrollment-empty">{t('servers.enrollment.noInvitations')}</div></td></tr>}
          </tbody></table></div>
        </section>

        <section className="section data-section">
          <div className="panel-header"><div><h3>{t('servers.enrollment.requestsTitle')}</h3><div className="panel-caption">{t('servers.enrollment.requestsCaption')}</div></div><span className="tag tag-warning">{enrollmentRequests.filter(item => item.request.state === 'pending').length}</span></div>
          <div className="table-wrap"><table className="data-table compact-table"><thead><tr><th>{t('servers.enrollment.node')}</th><th>{t('servers.enrollment.machine')}</th><th>{t('servers.enrollment.requestedAt')}</th><th>{t('servers.credentials.state')}</th><th>{t('servers.list.operations')}</th></tr></thead><tbody>
            {enrollmentRequests.map(item => <tr key={item.request.id}>
              <td><strong>{item.request.requested_name}</strong><div><code>{item.request.requested_node_uuid}</code></div></td>
              <td>{item.request.hostname || '-'}<div className="muted-text">{item.request.os || '-'} / {item.request.arch || '-'}</div></td>
              <td>{formatDateTime(item.request.created_at)}</td>
              <td><span className={`tag tag-${REQUEST_STATE_TONE[item.request.state] || 'info'}`}>{t(`servers.enrollment.requestState.${item.request.state}`)}</span></td>
              <td><button className="icon-button compact" type="button" title={t('servers.enrollment.review')} onClick={() => openRequest(item.request.id)}><ShieldCheck size={15} /></button></td>
            </tr>)}
            {enrollmentRequests.length === 0 && <tr><td colSpan={5}><div className="enrollment-empty">{t('servers.enrollment.noRequests')}</div></td></tr>}
          </tbody></table></div>
        </section>
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
                          {server.node_uuid && <code className="node-uuid">{server.node_uuid}</code>}
                          {server.node_uuid && <div className="node-state-row">
                            <span className={`tag tag-${server.enrollment_state === 'approved' ? 'success' : 'danger'}`}>{t(`servers.nodeState.enrollment.${server.enrollment_state}`)}</span>
                            <span className={`tag tag-${server.connection_state === 'connected' ? 'success' : 'info'}`}>{t(`servers.nodeState.connection.${server.connection_state}`)}</span>
                            <span className={`tag tag-${server.readiness_state === 'ready' ? 'success' : 'warning'}`}>{t(`servers.nodeState.readiness.${server.readiness_state}`)}</span>
                            <span className={`tag tag-${server.allocation_state === 'active' ? 'success' : 'warning'}`}>{t(`servers.nodeState.allocation.${server.allocation_state}`)}</span>
                          </div>}
                        </div>
                      </div>
                    </td>
                    <td>
                      <code>{server.api_host || '-'}</code>
                      {server.public_host && <div className="muted-text">{server.public_host}</div>}
                      {server.transport_mode && <div className="muted-text">{server.transport_mode}{server.agent_version ? ` · ${server.agent_version}` : ''}</div>}
                    </td>
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
                        {server.node_uuid && <button className="icon-button compact" type="button" title={t('servers.credentials.manage')} onClick={() => openCredentials(server)}><KeyRound size={15} /></button>}
                        <button className="icon-button compact danger" type="button" disabled={server.connection_state !== 'connected'} title={t(server.connection_state === 'connected' ? 'servers.connection.disconnect' : 'servers.connection.unavailable')} onClick={() => disconnectNode(server)}><Unplug size={15} /></button>
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
      {invitationDrawer && <InvitationDrawer form={invitationForm} servers={servers} saving={enrollmentBusy} onChange={setInvitationForm} onSubmit={createInvitation} onClose={() => setInvitationDrawer(false)} />}
      {requestDetails && <RequestDialog details={requestDetails} busy={enrollmentBusy} onApprove={() => reviewRequest('approve')} onReject={() => reviewRequest('reject')} onClose={() => setRequestDetails(null)} />}
      {credentialDialog && <CredentialDialog {...credentialDialog} busy={enrollmentBusy} onRotate={rotateCredential} onRevoke={revokeCredentials} onClose={() => setCredentialDialog(null)} />}
      {secretDialog && <SecretDialog {...secretDialog} onCopy={copySecret} onClose={() => setSecretDialog(null)} />}
      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
