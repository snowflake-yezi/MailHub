import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Activity,
  BellRing,
  CheckCircle2,
  Database,
  HardDrive,
  HeartPulse,
  KeyRound,
  MailCheck,
	FileText,
  RotateCcw,
  Save,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  TimerReset,
  Undo2,
  UserRound,
  X,
} from 'lucide-react'
import { accountAPI, configAPI, serverAPI } from '../api'
import ConfigDrawer from '../components/ConfigDrawer'
import ConfigField from '../components/ConfigField'
import NodeConfigDrawer from '../components/NodeConfigDrawer'
import { formatDateTime } from '../i18n'

const CATEGORY_META = {
  forward: { icon: MailCheck },
	mime: { icon: FileText },
  filter: { icon: SlidersHorizontal },
  lifecycle: { icon: TimerReset },
  healthcheck: { icon: HeartPulse },
  heartbeat: { icon: Activity },
  session: { icon: ShieldCheck },
  database: { icon: Database },
  maildir: { icon: HardDrive },
  general: { icon: Settings },
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])

  return <div className={`toast toast-${type}`}>{message}</div>
}

function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }) {
  const { t } = useTranslation('common')
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal confirm-modal" onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onCancel}>{t('actions.cancel')}</button>
          <button className="btn btn-danger" type="button" onClick={onConfirm}>{confirmLabel || t('actions.confirm')}</button>
        </div>
      </div>
    </div>
  )
}

function getCategoryMeta(group, t) {
  const meta = CATEGORY_META[group.category] || {}
  return {
    label: t(`config.categories.${group.category}.label`, { defaultValue: group.label || group.category }),
    desc: t(`config.categories.${group.category}.desc`, { defaultValue: t('config.module.genericDesc') }),
    icon: meta.icon || Settings,
  }
}

function getCategoryLabel(category, t) {
  return t(`config.categories.${category}.label`, { defaultValue: category })
}

function ModulePanel({ group, onConfigure }) {
  const { t } = useTranslation('pages')
  const meta = getCategoryMeta(group, t)
  const Icon = meta.icon
  const total = group.items.length
  const reloadable = group.items.filter(item => item.reloadable).length
  const restartRequired = group.items.filter(item => item.effect_type === 'restart' || (!item.effect_type && !item.reloadable)).length

  return (
    <article className="module-panel">
      <div className="module-main">
        <span className="module-icon"><Icon size={20} /></span>
        <div>
          <div className="module-title-row">
            <h3>{meta.label}</h3>
            <span className="status-badge status-active">
              <CheckCircle2 size={13} /> {t('config.module.enabled')}
            </span>
          </div>
          <p>{meta.desc}</p>
          <code>{group.category}</code>
        </div>
      </div>

      <div className="module-stats">
        <div>
          <strong>{total}</strong>
          <span>{t('config.module.params')}</span>
        </div>
        <div>
          <strong>{reloadable}</strong>
          <span>{t('config.module.hotReload')}</span>
        </div>
        <div data-warning={restartRequired > 0}>
          <strong>{restartRequired}</strong>
          <span>{t('config.module.restart')}</span>
        </div>
      </div>

      <div className="module-actions">
        <button className="btn btn-outline" type="button" onClick={() => onConfigure(group)}>
          <SlidersHorizontal size={16} /> {t('config.module.configure')}
        </button>
      </div>
    </article>
  )
}

function AccountPanel({ account, onEdit }) {
  const { t } = useTranslation('pages')
  return (
    <section className="account-panel">
      <div className="account-main">
        <span className="module-icon"><UserRound size={20} /></span>
        <div>
          <div className="module-title-row">
            <h3>{t('config.account.title')}</h3>
            {account.must_change_password && <span className="tag tag-warning">{t('config.account.changeRequired')}</span>}
          </div>
          <p>{t('config.account.description')}</p>
        </div>
      </div>
      <div className="account-meta">
        <div><span>{t('config.account.currentUsername')}</span><strong>{account.username}</strong></div>
        <div><span>{t('config.account.lastChanged')}</span><strong>{account.password_changed_at ? formatDateTime(account.password_changed_at) : t('config.account.noRecord')}</strong></div>
      </div>
      <button className="btn btn-outline" type="button" onClick={onEdit}>
        <KeyRound size={16} /> {t('config.account.settings')}
      </button>
    </section>
  )
}

function AccountDialog({ account, onClose }) {
  const { t } = useTranslation('pages')
  const [username, setUsername] = useState(account.username)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const submit = async (e) => {
    e.preventDefault()
    setError('')
    if (newPassword !== confirmPassword) {
      setError(t('config.account.mismatch'))
      return
    }
    setSaving(true)
    try {
      await accountAPI.update({
        username: username.trim(),
        current_password: currentPassword,
        new_password: newPassword,
      })
      window.location.assign('/admin/logout')
    } catch (e) {
      setError(e.message)
      setSaving(false)
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal account-modal" onClick={e => e.stopPropagation()}>
        <div className="account-modal-header">
          <div>
            <span>{t('config.account.kicker')}</span>
            <h3>{t('config.account.settings')}</h3>
          </div>
          <button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button>
        </div>
        {error && <div className="inline-alert" role="alert">{error}</div>}
        <form onSubmit={submit}>
          <div className="form-group">
            <label htmlFor="account-username">{t('config.account.username')}</label>
            <input id="account-username" value={username} onChange={e => setUsername(e.target.value)} required />
          </div>
          <div className="form-group">
            <label htmlFor="account-current-password">{t('config.account.currentPassword')}</label>
            <input id="account-current-password" type="password" value={currentPassword} onChange={e => setCurrentPassword(e.target.value)} autoComplete="current-password" required />
          </div>
          <div className="form-group">
            <label htmlFor="account-new-password">{t('config.account.newPassword')}</label>
            <input id="account-new-password" type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} autoComplete="new-password" placeholder={t('config.account.passwordPlaceholder')} />
            <div className="form-hint">{t('config.account.passwordHint')}</div>
          </div>
          <div className="form-group">
            <label htmlFor="account-confirm-password">{t('config.account.confirmPassword')}</label>
            <input id="account-confirm-password" type="password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} autoComplete="new-password" disabled={!newPassword} />
          </div>
          <div className="modal-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving ? <span className="spinner" /> : <Save size={16} />}
              {t('config.account.saveAndLogin')}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

function GlobalConfigDrawer({ group, onSave, onReset, onClose }) {
  const { t } = useTranslation('pages')
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState(false)
  const meta = getCategoryMeta(group, t)
  const Icon = meta.icon

  useEffect(() => {
    const initial = {}
    group.items.forEach(item => { initial[item.key] = item.value })
    setValues(initial)
  }, [group])

  const dirtyCount = useMemo(() => {
    return group.items.filter(item => values[item.key] !== undefined && values[item.key] !== item.value).length
  }, [group.items, values])

  const updateValue = (key, value) => {
    setValues(prev => ({ ...prev, [key]: value }))
  }

  const handleSave = async (e) => {
    e.preventDefault()
    const updates = {}
    group.items.forEach(item => {
      if (values[item.key] !== item.value) {
        updates[item.key] = values[item.key]
      }
    })

    if (Object.keys(updates).length === 0) {
      onClose()
      return
    }

    setSaving(true)
    try {
      await onSave(updates)
      onClose()
    } finally {
      setSaving(false)
    }
  }

  return (
    <ConfigDrawer title={meta.label} kicker={group.category} icon={Icon} ariaLabel={t('config.drawer.ariaLabel', { name: meta.label })} onClose={onClose}>
        <form className="drawer-body" onSubmit={handleSave}>
          <div className="config-drawer-summary">
            <span className="status-badge status-active">
              <CheckCircle2 size={13} /> {t('config.module.enabled')}
            </span>
            <span className={dirtyCount > 0 ? 'tag tag-warning' : 'tag tag-success'}>
              {dirtyCount > 0 ? t('config.drawer.dirty', { count: dirtyCount }) : t('config.drawer.noChanges')}
            </span>
          </div>

          <div className="config-field-list">
            {group.items.map(item => (
              <ConfigField key={item.key} item={item} value={values[item.key]} onChange={value => updateValue(item.key, value)} />
            ))}
          </div>

          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={() => onReset(group.category)}>
              <Undo2 size={16} /> {t('common:actions.resetDefault')}
            </button>
            <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving ? <span className="spinner" /> : <Save size={16} />}
              {t('config.drawer.saveParams')}
            </button>
          </div>
        </form>
    </ConfigDrawer>
  )
}

export default function ConfigPage() {
  const { t } = useTranslation('pages')
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedServerID = searchParams.get('server_id') || ''
  const [globalGroups, setGlobalGroups] = useState([])
  const [servers, setServers] = useState([])
  const [nodeData, setNodeData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [activeGroup, setActiveGroup] = useState(null)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [reloading, setReloading] = useState(false)
  const [account, setAccount] = useState(null)
  const [accountOpen, setAccountOpen] = useState(false)

  const loadConfigs = useCallback(async () => {
    try {
      const [configData, serverData] = await Promise.all([configAPI.list(), serverAPI.list()])
      setGlobalGroups(configData.groups || [])
      setServers(Array.isArray(serverData) ? serverData : [])
    } catch (e) {
      setToast({ type: 'error', message: t('config.messages.configLoadFailed', { message: e.message }) })
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { loadConfigs() }, [loadConfigs])

  const loadNodeConfigs = useCallback(async () => {
    if (!selectedServerID) {
      setNodeData(null)
      return
    }
    try {
      setNodeData(await serverAPI.configs(selectedServerID))
    } catch (e) {
      setToast({ type: 'error', message: t('config.messages.nodeLoadFailed', { message: e.message }) })
      setNodeData(null)
    }
  }, [selectedServerID, t])

  useEffect(() => { loadNodeConfigs() }, [loadNodeConfigs])

  const selectedServer = useMemo(
    () => servers.find(server => String(server.id) === String(selectedServerID)),
    [selectedServerID, servers],
  )

  const groups = useMemo(() => {
    if (!selectedServerID) return globalGroups
    const grouped = new Map()
    for (const item of nodeData?.items || []) {
      if (!grouped.has(item.category)) grouped.set(item.category, { category: item.category, label: getCategoryLabel(item.category, t), items: [] })
      grouped.get(item.category).items.push({ ...item, value: item.override_value ?? item.global_value })
    }
    return Array.from(grouped.values())
  }, [globalGroups, nodeData, selectedServerID, t])

  useEffect(() => {
    accountAPI.get().then(data => {
      setAccount(data)
      if (data.must_change_password || searchParams.get('account') === 'required') setAccountOpen(true)
    }).catch(e => setToast({ type: 'error', message: t('config.messages.accountLoadFailed', { message: e.message }) }))
  }, [searchParams, t])

  const closeAccount = () => {
    setAccountOpen(false)
    if (searchParams.has('account')) {
      const next = new URLSearchParams(searchParams)
      next.delete('account')
      setSearchParams(next, { replace: true })
    }
  }

  const stats = useMemo(() => {
    const totalParams = groups.reduce((sum, group) => sum + group.items.length, 0)
    const reloadable = groups.reduce((sum, group) => sum + group.items.filter(item => item.effect_type === 'hot_reload' || (!item.effect_type && item.reloadable)).length, 0)
    const restartRequired = groups.reduce((sum, group) => sum + group.items.filter(item => item.effect_type === 'restart' || (!item.effect_type && !item.reloadable)).length, 0)
    return {
      modules: groups.length,
      totalParams,
      reloadable,
      restartRequired,
    }
  }, [groups])

  const handleSaveGroup = async (updates) => {
    try {
      await configAPI.batchUpdate(updates)
      setToast({ type: 'success', message: t('config.messages.saved', { count: Object.keys(updates).length }) })
      await loadConfigs()
    } catch (e) {
      setToast({ type: 'error', message: t('common:errors.saveFailed', { message: e.message }) })
      throw e
    }
  }

  const handleResetGroup = (category) => {
    setConfirm({
      title: t('config.dialogs.resetTitle'),
      message: t('config.dialogs.resetMessage', { name: getCategoryLabel(category, t) }),
      confirmLabel: t('common:actions.resetDefault'),
      onConfirm: async () => {
        try {
          const group = globalGroups.find(item => item.category === category)
          if (group) {
            await Promise.all(group.items.map(item => configAPI.reset(item.key)))
          }
          setToast({ type: 'success', message: t('config.messages.resetDone') })
          setActiveGroup(null)
          await loadConfigs()
        } catch (e) {
          setToast({ type: 'error', message: t('config.messages.resetFailed', { message: e.message }) })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const handleReload = async () => {
    setReloading(true)
    try {
      await configAPI.reload()
      setToast({ type: 'success', message: t('config.messages.reloadSent') })
    } catch (e) {
      setToast({ type: 'error', message: t('config.messages.operationFailed', { message: e.message }) })
    } finally {
      setReloading(false)
    }
  }

  const refreshScope = async () => {
    if (selectedServerID) await loadNodeConfigs()
    else await loadConfigs()
  }

  const changeScope = event => {
    const next = new URLSearchParams(searchParams)
    if (event.target.value) next.set('server_id', event.target.value)
    else next.delete('server_id')
    setActiveGroup(null)
    setSearchParams(next, { replace: true })
  }

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> {t('config.loading')}
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('config.title')}</h1>
          <p className="page-subtitle">{t('config.subtitle')}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={refreshScope}>
            <RotateCcw size={16} /> {t('common:actions.refresh')}
          </button>
          {!selectedServerID && <button className="btn btn-primary" type="button" onClick={handleReload} disabled={reloading}>
            {reloading ? <span className="spinner" /> : <BellRing size={16} />}
            {t('config.reloadNodes')}
          </button>}
        </div>
      </div>

      <section className="scope-toolbar" aria-label={t('config.scope.ariaLabel')}>
        <div className="scope-copy">
          <span>{t('config.scope.label')}</span>
          <strong>{selectedServer ? selectedServer.name : t('config.scope.global')}</strong>
          <small>{selectedServer ? t('config.scope.nodeHint') : t('config.scope.globalHint')}</small>
        </div>
        <select value={selectedServerID} onChange={changeScope} aria-label={t('config.scope.selectAria')}>
          <option value="">{t('config.scope.global')}</option>
          {servers.map(server => <option key={server.id} value={server.id}>{server.name || `server-${server.id}`}</option>)}
        </select>
      </section>

      <div className="summary-grid config-summary">
        <div className="summary-tile" data-tone="brand">
          <span className="summary-icon"><Settings size={18} /></span>
          <div>
            <div className="summary-value">{stats.modules}</div>
            <div className="summary-label">{t('config.summary.modules')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="info">
          <span className="summary-icon"><SlidersHorizontal size={18} /></span>
          <div>
            <div className="summary-value">{stats.totalParams}</div>
            <div className="summary-label">{t('config.summary.params')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="success">
          <span className="summary-icon"><CheckCircle2 size={18} /></span>
          <div>
            <div className="summary-value">{stats.reloadable}</div>
            <div className="summary-label">{t('config.summary.hotReload')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="warning">
          <span className="summary-icon"><AlertTriangleIcon /></span>
          <div>
            <div className="summary-value">{stats.restartRequired}</div>
            <div className="summary-label">{t('config.summary.restart')}</div>
          </div>
        </div>
      </div>

      {!selectedServerID && account && <AccountPanel account={account} onEdit={() => setAccountOpen(true)} />}

      {groups.length > 0 ? (
        <div className="module-grid">
          {groups.map(group => (
            <ModulePanel key={group.category} group={group} onConfigure={setActiveGroup} />
          ))}
        </div>
      ) : (
        <section className="section empty-state">
          <Database size={30} />
          <strong>{t('config.empty.title')}</strong>
          <span>{t('config.empty.description')}</span>
        </section>
      )}

      {activeGroup && (
        selectedServer ? (
          <NodeConfigDrawer
            server={selectedServer}
            category={activeGroup.category}
            onToast={setToast}
            onChanged={loadNodeConfigs}
            onClose={() => setActiveGroup(null)}
          />
        ) : (
          <GlobalConfigDrawer
            group={activeGroup}
            onSave={handleSaveGroup}
            onReset={handleResetGroup}
            onClose={() => setActiveGroup(null)}
          />
        )
      )}
      {confirm && <ConfirmDialog {...confirm} />}
      {accountOpen && account && <AccountDialog account={account} onClose={closeAccount} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}

function AlertTriangleIcon() {
  return <TimerReset size={18} />
}
