import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Filter,
  Flag,
  Pencil,
  Plus,
  RefreshCw,
  ShieldOff,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-react'
import { filterAPI } from '../api'

const RULE_TYPES = [
  { value: 'whitelist_sender', label: '发件人白名单' },
  { value: 'blacklist_sender', label: '发件人黑名单' },
  { value: 'keyword', label: '关键词' },
  { value: 'regex', label: '正则表达式' },
]

const ACTIONS = [
  { value: 'pass', label: 'pass 放行转发', icon: CheckCircle2, tone: 'success' },
  { value: 'flag', label: 'flag 标记疑似', icon: Flag, tone: 'warning' },
  { value: 'block', label: 'block 直接丢弃', icon: ShieldOff, tone: 'danger' },
]

const TYPE_TAG = {
  whitelist_sender: 'tag-success',
  blacklist_sender: 'tag-danger',
  keyword: 'tag-info',
  regex: 'tag-warning',
}

const ACTION_TAG = {
  pass: 'tag-success',
  flag: 'tag-warning',
  block: 'tag-danger',
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

function FilterDrawer({ modal, saving, onChange, onSave, onClose }) {
  const updateField = (field, value) => {
    onChange({ ...modal, data: { ...modal.data, [field]: value } })
  }

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer" onClick={e => e.stopPropagation()} aria-label={modal.mode === 'add' ? '新增过滤规则' : '编辑过滤规则'}>
        <div className="drawer-header">
          <div className="drawer-title-with-icon">
            <span className="module-icon"><SlidersHorizontal size={18} /></span>
            <div>
              <div className="drawer-kicker">Filter policy</div>
              <h2>{modal.mode === 'add' ? '新增过滤规则' : `编辑 ${modal.data.name || `#${modal.data.id}`}`}</h2>
            </div>
          </div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}><X size={18} /></button>
        </div>

        <form className="drawer-body" onSubmit={onSave}>
          <div className="form-group">
            <label>规则名称</label>
            <input value={modal.data.name} onChange={e => updateField('name', e.target.value)} placeholder="例如：白名单-航司通知" required />
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>类型</label>
              <select value={modal.data.rule_type} onChange={e => updateField('rule_type', e.target.value)}>
                {RULE_TYPES.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label>动作</label>
              <select value={modal.data.action} onChange={e => updateField('action', e.target.value)}>
                {ACTIONS.map(a => <option key={a.value} value={a.value}>{a.label}</option>)}
              </select>
            </div>
          </div>
          <div className="form-group">
            <label>匹配模式</label>
            <input value={modal.data.pattern} onChange={e => updateField('pattern', e.target.value)} placeholder="@airline.com / 行程单 / (?i)itinerary" required />
            <div className="form-hint">白名单和黑名单通常填写发件人域名；关键词填写要匹配的词；正则使用 Go 正则表达式。</div>
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>优先级</label>
              <input type="number" value={modal.data.priority} onChange={e => updateField('priority', parseInt(e.target.value, 10) || 0)} min={0} />
              <div className="form-hint">数字越小越先匹配。</div>
            </div>
            <div className="form-group">
              <label>启用状态</label>
              <label className="switch-row">
                <span className="toggle">
                  <input type="checkbox" checked={modal.data.enabled} onChange={e => updateField('enabled', e.target.checked)} />
                  <span className="toggle-slider" />
                </span>
                {modal.data.enabled ? '启用' : '停用'}
              </label>
            </div>
          </div>

          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>取消</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving && <span className="spinner" />} 保存
            </button>
          </div>
        </form>
      </aside>
    </div>
  )
}

export default function FiltersPage() {
  const [rules, setRules] = useState([])
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [modal, setModal] = useState(null)
  const [saving, setSaving] = useState(false)
  const [actionFilter, setActionFilter] = useState('all')

  const load = useCallback((silent = false) => {
    if (!silent) setLoading(true)
    filterAPI.list()
      .then(data => setRules(Array.isArray(data) ? data : []))
      .catch(e => setToast({ type: 'error', message: '加载失败: ' + e.message }))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => {
    const enabled = rules.filter(r => r.enabled).length
    const byAction = rules.reduce((acc, rule) => {
      acc[rule.action] = (acc[rule.action] || 0) + 1
      return acc
    }, {})
    return {
      total: rules.length,
      enabled,
      pass: byAction.pass || 0,
      flag: byAction.flag || 0,
      block: byAction.block || 0,
    }
  }, [rules])

  const visibleRules = useMemo(() => {
    const sorted = [...rules].sort((a, b) => (a.priority || 0) - (b.priority || 0))
    return actionFilter === 'all' ? sorted : sorted.filter(r => r.action === actionFilter)
  }, [rules, actionFilter])

  const openAdd = () => setModal({
    mode: 'add',
    data: { name: '', rule_type: 'whitelist_sender', pattern: '', action: 'pass', priority: 10, enabled: true },
  })

  const openEdit = (r) => setModal({ mode: 'edit', data: { ...r } })

  const handleSave = async (e) => {
    e.preventDefault()
    setSaving(true)
    try {
      if (modal.mode === 'add') await filterAPI.create(modal.data)
      else await filterAPI.update(modal.data.id, modal.data)
      setModal(null)
      setToast({ type: 'success', message: '保存成功' })
      load(true)
    } catch (err) {
      setToast({ type: 'error', message: err.message })
    } finally {
      setSaving(false)
    }
  }

  const askDelete = (rule) => {
    setConfirm({
      title: '删除过滤规则',
      message: `确定删除「${rule.name}」吗？节点下一次拉取配置后将不再应用这条规则。`,
      confirmLabel: '删除',
      onConfirm: async () => {
        try {
          await filterAPI.remove(rule.id)
          setToast({ type: 'success', message: '已删除' })
          load(true)
        } catch (err) {
          setToast({ type: 'error', message: err.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const toggleEnabled = async (rule) => {
    try {
      await filterAPI.update(rule.id, { ...rule, enabled: !rule.enabled })
      setToast({ type: 'success', message: rule.enabled ? '规则已停用' : '规则已启用' })
      load(true)
    } catch (err) {
      setToast({ type: 'error', message: err.message })
    }
  }

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> 加载过滤规则...
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>过滤规则</h1>
          <p className="page-subtitle">按 pass、flag、block 管理过滤策略；优先级数字越小越先匹配。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => load(true)}>
            <RefreshCw size={16} /> 刷新
          </button>
          <button className="btn btn-primary" type="button" onClick={openAdd}>
            <Plus size={16} /> 新增规则
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <SummaryTile icon={Filter} label="规则总数" value={summary.total} tone="brand" />
        <SummaryTile icon={CheckCircle2} label="已启用" value={summary.enabled} tone="success" />
        <SummaryTile icon={Flag} label="标记疑似" value={summary.flag} tone="warning" />
        <SummaryTile icon={ShieldOff} label="直接丢弃" value={summary.block} tone="danger" />
      </div>

      <div className="phase-tabs policy-tabs">
        <button className={actionFilter === 'all' ? 'active' : ''} type="button" onClick={() => setActionFilter('all')}>全部</button>
        {ACTIONS.map(action => (
          <button className={actionFilter === action.value ? 'active' : ''} type="button" key={action.value} onClick={() => setActionFilter(action.value)}>
            {action.label}
          </button>
        ))}
      </div>

      <section className="section data-section">
        <div className="panel-header">
          <div>
            <h3>策略列表</h3>
            <div className="panel-caption">修改后 mail-node 会按配置拉取周期自动生效。</div>
          </div>
        </div>

        <div className="table-wrap">
          <table className="data-table filters-table">
            <thead>
              <tr>
                <th>优先级</th>
                <th>规则</th>
                <th>类型</th>
                <th>匹配模式</th>
                <th>动作</th>
                <th>启用</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {visibleRules.map(rule => (
                <tr key={rule.id}>
                  <td><span className="priority-pill">{rule.priority}</span></td>
                  <td>
                    <div className="entity-cell">
                      <span className="entity-icon"><SlidersHorizontal size={16} /></span>
                      <div>
                        <strong>{rule.name}</strong>
                        <span>#{rule.id}</span>
                      </div>
                    </div>
                  </td>
                  <td><span className={`tag ${TYPE_TAG[rule.rule_type] || 'tag-info'}`}>{RULE_TYPES.find(t => t.value === rule.rule_type)?.label || rule.rule_type}</span></td>
                  <td><code>{rule.pattern}</code></td>
                  <td><span className={`tag ${ACTION_TAG[rule.action] || 'tag-info'}`}>{rule.action}</span></td>
                  <td>
                    <label className="switch-row compact-switch">
                      <span className="toggle">
                        <input type="checkbox" checked={rule.enabled} onChange={() => toggleEnabled(rule)} />
                        <span className="toggle-slider" />
                      </span>
                    </label>
                  </td>
                  <td>
                    <div className="row-actions">
                      <button className="icon-button compact" type="button" title="编辑" onClick={() => openEdit(rule)}>
                        <Pencil size={15} />
                      </button>
                      <button className="icon-button compact danger" type="button" title="删除" onClick={() => askDelete(rule)}>
                        <Trash2 size={15} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {visibleRules.length === 0 && (
                <tr>
                  <td colSpan={7}>
                    <div className="empty-state">
                      <AlertTriangle size={28} />
                      <strong>暂无匹配规则</strong>
                      <span>新增规则后即可按策略过滤和转发邮件。</span>
                      <button className="btn btn-primary" type="button" onClick={openAdd}><Plus size={16} /> 新增规则</button>
                    </div>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {modal && <FilterDrawer modal={modal} saving={saving} onChange={setModal} onSave={handleSave} onClose={() => setModal(null)} />}
      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
