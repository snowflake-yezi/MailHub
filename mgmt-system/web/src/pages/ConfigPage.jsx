import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Activity,
  BellRing,
  CheckCircle2,
  Database,
  HardDrive,
  HeartPulse,
  KeyRound,
  MailCheck,
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

const CATEGORY_META = {
  forward: { label: '邮件转发引擎', desc: '控制 union 转发目标、重试与链路行为。', icon: MailCheck },
  filter: { label: '过滤引擎', desc: '控制规则匹配、默认动作和热加载策略。', icon: SlidersHorizontal },
  lifecycle: { label: '生命周期管理', desc: '控制邮箱禁用、回收与清理节奏。', icon: TimerReset },
  healthcheck: { label: '健康检查', desc: '控制主动探测、失败阈值和状态降级。', icon: HeartPulse },
  heartbeat: { label: '心跳上报', desc: '控制 mail-node 心跳间隔和节点存活判定。', icon: Activity },
  session: { label: '管理会话', desc: '控制后台登录会话和安全策略。', icon: ShieldCheck },
  database: { label: '数据库连接池', desc: '控制连接池上限、空闲连接和超时。', icon: Database },
  maildir: { label: '邮件存储', desc: '控制 Maildir 路径、保留和存储行为。', icon: HardDrive },
  general: { label: '通用参数', desc: '系统级基础配置和默认行为。', icon: Settings },
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])

  return <div className={`toast toast-${type}`}>{message}</div>
}

function ConfirmDialog({ title, message, confirmLabel = '确认', onConfirm, onCancel }) {
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal confirm-modal" onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onCancel}>取消</button>
          <button className="btn btn-danger" type="button" onClick={onConfirm}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}

function getCategoryMeta(group) {
  const meta = CATEGORY_META[group.category] || {}
  return {
    label: group.label || meta.label || group.category,
    desc: meta.desc || '集中管理该模块的运行参数。',
    icon: meta.icon || Settings,
  }
}

function getCategoryLabel(category) {
  return CATEGORY_META[category]?.label || category
}

function ModulePanel({ group, onConfigure }) {
  const meta = getCategoryMeta(group)
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
              <CheckCircle2 size={13} /> 启用中
            </span>
          </div>
          <p>{meta.desc}</p>
          <code>{group.category}</code>
        </div>
      </div>

      <div className="module-stats">
        <div>
          <strong>{total}</strong>
          <span>参数</span>
        </div>
        <div>
          <strong>{reloadable}</strong>
          <span>热加载</span>
        </div>
        <div data-warning={restartRequired > 0}>
          <strong>{restartRequired}</strong>
          <span>需重启</span>
        </div>
      </div>

      <div className="module-actions">
        <button className="btn btn-outline" type="button" onClick={() => onConfigure(group)}>
          <SlidersHorizontal size={16} /> 参数配置
        </button>
      </div>
    </article>
  )
}

function AccountPanel({ account, onEdit }) {
  return (
    <section className="account-panel">
      <div className="account-main">
        <span className="module-icon"><UserRound size={20} /></span>
        <div>
          <div className="module-title-row">
            <h3>管理账号</h3>
            {account.must_change_password && <span className="tag tag-warning">需要修改密码</span>}
          </div>
          <p>管理登录身份与恢复后的凭据状态。</p>
        </div>
      </div>
      <div className="account-meta">
        <div><span>当前用户名</span><strong>{account.username}</strong></div>
        <div><span>最近改密</span><strong>{account.password_changed_at ? new Date(account.password_changed_at).toLocaleString() : '尚无记录'}</strong></div>
      </div>
      <button className="btn btn-outline" type="button" onClick={onEdit}>
        <KeyRound size={16} /> 账号设置
      </button>
    </section>
  )
}

function AccountDialog({ account, onClose }) {
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
      setError('两次输入的新密码不一致')
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
            <span>账号安全</span>
            <h3>管理账号设置</h3>
          </div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}><X size={18} /></button>
        </div>
        {error && <div className="inline-alert" role="alert">{error}</div>}
        <form onSubmit={submit}>
          <div className="form-group">
            <label htmlFor="account-username">用户名</label>
            <input id="account-username" value={username} onChange={e => setUsername(e.target.value)} required />
          </div>
          <div className="form-group">
            <label htmlFor="account-current-password">当前密码</label>
            <input id="account-current-password" type="password" value={currentPassword} onChange={e => setCurrentPassword(e.target.value)} autoComplete="current-password" required />
          </div>
          <div className="form-group">
            <label htmlFor="account-new-password">新密码</label>
            <input id="account-new-password" type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} autoComplete="new-password" placeholder="留空表示不修改密码" />
            <div className="form-hint">生产环境至少 12 位，不能使用常见弱密码或与用户名相同。</div>
          </div>
          <div className="form-group">
            <label htmlFor="account-confirm-password">确认新密码</label>
            <input id="account-confirm-password" type="password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} autoComplete="new-password" disabled={!newPassword} />
          </div>
          <div className="modal-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>取消</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving ? <span className="spinner" /> : <Save size={16} />}
              保存并重新登录
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

function GlobalConfigDrawer({ group, onSave, onReset, onClose }) {
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState(false)
  const meta = getCategoryMeta(group)
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
    <ConfigDrawer title={meta.label} kicker={group.category} icon={Icon} ariaLabel={`${meta.label} 参数配置`} onClose={onClose}>
        <form className="drawer-body" onSubmit={handleSave}>
          <div className="config-drawer-summary">
            <span className="status-badge status-active">
              <CheckCircle2 size={13} /> 启用中
            </span>
            <span className={dirtyCount > 0 ? 'tag tag-warning' : 'tag tag-success'}>
              {dirtyCount > 0 ? `${dirtyCount} 项未保存` : '无未保存修改'}
            </span>
          </div>

          <div className="config-field-list">
            {group.items.map(item => (
              <ConfigField key={item.key} item={item} value={values[item.key]} onChange={value => updateValue(item.key, value)} />
            ))}
          </div>

          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={() => onReset(group.category)}>
              <Undo2 size={16} /> 恢复默认
            </button>
            <button className="btn btn-outline" type="button" onClick={onClose}>取消</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving ? <span className="spinner" /> : <Save size={16} />}
              保存参数
            </button>
          </div>
        </form>
    </ConfigDrawer>
  )
}

export default function ConfigPage() {
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
      setToast({ type: 'error', message: '加载配置失败: ' + e.message })
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { loadConfigs() }, [loadConfigs])

  const loadNodeConfigs = useCallback(async () => {
    if (!selectedServerID) {
      setNodeData(null)
      return
    }
    try {
      setNodeData(await serverAPI.configs(selectedServerID))
    } catch (e) {
      setToast({ type: 'error', message: '加载节点配置失败: ' + e.message })
      setNodeData(null)
    }
  }, [selectedServerID])

  useEffect(() => { loadNodeConfigs() }, [loadNodeConfigs])

  const selectedServer = useMemo(
    () => servers.find(server => String(server.id) === String(selectedServerID)),
    [selectedServerID, servers],
  )

  const groups = useMemo(() => {
    if (!selectedServerID) return globalGroups
    const grouped = new Map()
    for (const item of nodeData?.items || []) {
      if (!grouped.has(item.category)) grouped.set(item.category, { category: item.category, label: getCategoryLabel(item.category), items: [] })
      grouped.get(item.category).items.push({ ...item, value: item.override_value ?? item.global_value })
    }
    return Array.from(grouped.values())
  }, [globalGroups, nodeData, selectedServerID])

  useEffect(() => {
    accountAPI.get().then(data => {
      setAccount(data)
      if (data.must_change_password || searchParams.get('account') === 'required') setAccountOpen(true)
    }).catch(e => setToast({ type: 'error', message: '加载管理账号失败: ' + e.message }))
  }, [searchParams])

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
      setToast({ type: 'success', message: `已保存 ${Object.keys(updates).length} 项配置` })
      await loadConfigs()
    } catch (e) {
      setToast({ type: 'error', message: '保存失败: ' + e.message })
      throw e
    }
  }

  const handleResetGroup = (category) => {
    setConfirm({
      title: '恢复默认配置',
      message: `确定要将「${getCategoryLabel(category)}」的所有参数恢复为默认值吗？此操作不可撤销。`,
      confirmLabel: '恢复默认',
      onConfirm: async () => {
        try {
          const group = globalGroups.find(item => item.category === category)
          if (group) {
            await Promise.all(group.items.map(item => configAPI.reset(item.key)))
          }
          setToast({ type: 'success', message: '已恢复默认值' })
          setActiveGroup(null)
          await loadConfigs()
        } catch (e) {
          setToast({ type: 'error', message: '恢复失败: ' + e.message })
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
      setToast({ type: 'success', message: '已通知所有节点重载配置' })
    } catch (e) {
      setToast({ type: 'error', message: '操作失败: ' + e.message })
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
        <span className="spinner" /> 加载系统配置...
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>系统配置</h1>
          <p className="page-subtitle">统一管理全局默认与节点覆盖，并对照节点实际上报的生效状态。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={refreshScope}>
            <RotateCcw size={16} /> 刷新
          </button>
          {!selectedServerID && <button className="btn btn-primary" type="button" onClick={handleReload} disabled={reloading}>
            {reloading ? <span className="spinner" /> : <BellRing size={16} />}
            通知节点重载
          </button>}
        </div>
      </div>

      <section className="scope-toolbar" aria-label="配置作用域">
        <div className="scope-copy">
          <span>作用域</span>
          <strong>{selectedServer ? selectedServer.name : '全局默认'}</strong>
          <small>{selectedServer ? '仅覆盖该节点，保存后自动通知热加载' : '作为所有节点的默认配置'}</small>
        </div>
        <select value={selectedServerID} onChange={changeScope} aria-label="选择配置作用域">
          <option value="">全局默认</option>
          {servers.map(server => <option key={server.id} value={server.id}>{server.name || `server-${server.id}`}</option>)}
        </select>
      </section>

      <div className="summary-grid config-summary">
        <div className="summary-tile" data-tone="brand">
          <span className="summary-icon"><Settings size={18} /></span>
          <div>
            <div className="summary-value">{stats.modules}</div>
            <div className="summary-label">配置模块</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="info">
          <span className="summary-icon"><SlidersHorizontal size={18} /></span>
          <div>
            <div className="summary-value">{stats.totalParams}</div>
            <div className="summary-label">可调参数</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="success">
          <span className="summary-icon"><CheckCircle2 size={18} /></span>
          <div>
            <div className="summary-value">{stats.reloadable}</div>
            <div className="summary-label">支持热加载</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="warning">
          <span className="summary-icon"><AlertTriangleIcon /></span>
          <div>
            <div className="summary-value">{stats.restartRequired}</div>
            <div className="summary-label">需重启生效</div>
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
          <strong>暂无配置数据</strong>
          <span>请检查数据库连接或配置初始化状态。</span>
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
