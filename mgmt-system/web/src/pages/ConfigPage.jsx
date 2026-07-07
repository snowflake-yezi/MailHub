import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity,
  BellRing,
  CheckCircle2,
  Database,
  HardDrive,
  HeartPulse,
  MailCheck,
  RotateCcw,
  Save,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  TimerReset,
  Undo2,
  X,
} from 'lucide-react'
import { configAPI } from '../api'

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
  const restartRequired = total - reloadable

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

function ConfigDrawer({ group, onSave, onReset, onClose }) {
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

  const renderInput = (item) => {
    const value = values[item.key] ?? item.value ?? ''
    if (item.value_type === 'bool') {
      return (
        <label className="toggle">
          <input
            type="checkbox"
            checked={value === 'true' || value === '1' || value === true}
            onChange={e => updateValue(item.key, e.target.checked ? 'true' : 'false')}
          />
          <span className="toggle-slider" />
        </label>
      )
    }

    if (item.value_type === 'int') {
      return (
        <input
          type="number"
          value={value}
          onChange={e => updateValue(item.key, e.target.value)}
        />
      )
    }

    if (String(value).length > 72) {
      return (
        <textarea
          rows={3}
          value={value}
          onChange={e => updateValue(item.key, e.target.value)}
        />
      )
    }

    return (
      <input
        type="text"
        value={value}
        onChange={e => updateValue(item.key, e.target.value)}
      />
    )
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
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer drawer-wide" onClick={e => e.stopPropagation()} aria-label={`${meta.label} 参数配置`}>
        <div className="drawer-header">
          <div className="drawer-title-with-icon">
            <span className="module-icon"><Icon size={20} /></span>
            <div>
              <div className="drawer-kicker">{group.category}</div>
              <h2>{meta.label}</h2>
            </div>
          </div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}>
            <X size={18} />
          </button>
        </div>

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
              <section className="config-field" key={item.key}>
                <div className="config-field-head">
                  <div>
                    <label>{item.label}</label>
                    <code>{item.key}</code>
                  </div>
                  {item.reloadable ? (
                    <span className="tag tag-info">热加载</span>
                  ) : (
                    <span className="tag tag-warning">需重启</span>
                  )}
                </div>
                {renderInput(item)}
                <div className="form-hint">
                  {item.description || '暂无说明'}
                  <span>默认: {item.default_value ?? '-'}</span>
                </div>
              </section>
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
      </aside>
    </div>
  )
}

export default function ConfigPage() {
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)
  const [activeGroup, setActiveGroup] = useState(null)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [reloading, setReloading] = useState(false)

  const loadConfigs = useCallback(async () => {
    try {
      const data = await configAPI.list()
      setGroups(data.groups || [])
    } catch (e) {
      setToast({ type: 'error', message: '加载配置失败: ' + e.message })
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { loadConfigs() }, [loadConfigs])

  const stats = useMemo(() => {
    const totalParams = groups.reduce((sum, group) => sum + group.items.length, 0)
    const reloadable = groups.reduce((sum, group) => sum + group.items.filter(item => item.reloadable).length, 0)
    return {
      modules: groups.length,
      totalParams,
      reloadable,
      restartRequired: totalParams - reloadable,
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
          const group = groups.find(item => item.category === category)
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
          <p className="page-subtitle">以模块为单位管理参数，区分热加载项和需重启项。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={loadConfigs}>
            <RotateCcw size={16} /> 刷新
          </button>
          <button className="btn btn-primary" type="button" onClick={handleReload} disabled={reloading}>
            {reloading ? <span className="spinner" /> : <BellRing size={16} />}
            通知节点重载
          </button>
        </div>
      </div>

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
        <ConfigDrawer
          group={activeGroup}
          onSave={handleSaveGroup}
          onReset={handleResetGroup}
          onClose={() => setActiveGroup(null)}
        />
      )}
      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}

function AlertTriangleIcon() {
  return <TimerReset size={18} />
}
