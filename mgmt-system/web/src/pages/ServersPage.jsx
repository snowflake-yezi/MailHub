import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  CircleOff,
  Database,
  Pencil,
  Plus,
  Power,
  RotateCcw,
  Server,
  Trash2,
  X,
} from 'lucide-react'
import { serverAPI } from '../api'

const STATUS_META = {
  healthy: { label: '健康', className: 'status-healthy', icon: CheckCircle2 },
  degraded: { label: '降级', className: 'status-degraded', icon: AlertTriangle },
  draining: { label: '缩容中', className: 'status-draining', icon: Activity },
  down: { label: '离线', className: 'status-down', icon: CircleOff },
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

function ConfirmDialog({ title, message, confirmLabel = '确认', danger = true, onConfirm, onCancel }) {
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal confirm-modal" onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onCancel}>取消</button>
          <button className={`btn ${danger ? 'btn-danger' : 'btn-primary'}`} type="button" onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

function StatusBadge({ status }) {
  const meta = STATUS_META[status] || STATUS_META.down
  const Icon = meta.icon
  return (
    <span className={`status-badge ${meta.className}`}>
      <Icon size={13} />
      {meta.label}
    </span>
  )
}

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
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
  const isEdit = mode === 'edit'

  const updateField = (field, value) => {
    onChange(prev => ({ ...prev, [field]: value }))
  }

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer" onClick={e => e.stopPropagation()} aria-label={isEdit ? '编辑服务器' : '注册服务器'}>
        <div className="drawer-header">
          <div>
            <div className="drawer-kicker">Mail node</div>
            <h2>{isEdit ? `编辑 ${form.name || `#${form.id}`}` : '注册服务器'}</h2>
          </div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}>
            <X size={18} />
          </button>
        </div>

        <form className="drawer-body" onSubmit={onSave}>
          <div className="form-group">
            <label>服务器名称</label>
            <input
              value={form.name}
              onChange={e => updateField('name', e.target.value)}
              placeholder="例如: mail-node-01"
              required
            />
          </div>
          <div className="form-group">
            <label>API 地址</label>
            <input
              value={form.api_host}
              onChange={e => updateField('api_host', e.target.value)}
              placeholder="例如: 10.0.0.2:8081"
              required
            />
            <div className="form-hint">SMTP / IMAP 地址由后端按 API 地址推导，注册后仍可调整节点状态。</div>
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>容量上限</label>
              <input
                type="number"
                min={1}
                value={form.capacity}
                onChange={e => updateField('capacity', parseInt(e.target.value, 10) || 0)}
              />
            </div>
            <div className="form-group">
              <label>心跳间隔</label>
              <input
                type="number"
                min={5}
                max={600}
                value={form.heartbeat_interval}
                onChange={e => updateField('heartbeat_interval', parseInt(e.target.value, 10) || 0)}
              />
              <div className="form-hint">单位：秒，建议 5-600。</div>
            </div>
          </div>
          {isEdit && (
            <div className="form-group">
              <label>运行状态</label>
              <select value={form.status} onChange={e => updateField('status', e.target.value)}>
                <option value="healthy">健康</option>
                <option value="degraded">降级</option>
                <option value="draining">缩容中</option>
                <option value="down">离线</option>
              </select>
            </div>
          )}

          <div className="drawer-footer">
            {isEdit && (
              <button className="btn btn-outline btn-danger-outline" type="button" onClick={onDelete}>
                <Trash2 size={16} /> 删除
              </button>
            )}
            <button className="btn btn-outline" type="button" onClick={onClose}>取消</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving && <span className="spinner" />}
              {isEdit ? '保存修改' : '注册服务器'}
            </button>
          </div>
        </form>
      </aside>
    </div>
  )
}

export default function ServersPage() {
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
      setToast({ type: 'error', message: '加载失败: ' + e.message })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => {
    const byStatus = servers.reduce((acc, server) => {
      acc[server.status || 'down'] = (acc[server.status || 'down'] || 0) + 1
      return acc
    }, {})
    return {
      total: servers.length,
      healthy: byStatus.healthy || 0,
      degraded: byStatus.degraded || 0,
      draining: byStatus.draining || 0,
      down: byStatus.down || 0,
    }
  }, [servers])

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
        setToast({ type: 'success', message: '服务器修改已保存' })
      } else {
        await serverAPI.create({
          name: payload.name,
          api_host: payload.api_host,
          capacity: payload.capacity,
        })
        setToast({ type: 'success', message: '服务器注册成功' })
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
    const action = newStatus === 'draining' ? '缩容' : '恢复服务'
    setConfirm({
      title: `${action}服务器`,
      message: `确定将「${server.name}」${action}吗？`,
      confirmLabel: action,
      danger: newStatus === 'draining',
      onConfirm: async () => {
        try {
          await serverAPI.update(server.id, { status: newStatus })
          setToast({ type: 'success', message: '状态已更新' })
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
      title: '删除服务器',
      message: `确定要删除「${server.name}」吗？此操作不可撤销。`,
      confirmLabel: '删除',
      onConfirm: async () => {
        try {
          await serverAPI.remove(server.id)
          setToast({ type: 'success', message: '服务器已删除' })
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
        <span className="spinner" /> 加载服务器池...
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>服务器池</h1>
          <p className="page-subtitle">管理 mail-node 节点、域名归属、容量水位与主动探测状态。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => load(true)} disabled={refreshing}>
            {refreshing ? <span className="spinner" /> : <RotateCcw size={16} />}
            刷新
          </button>
          <button className="btn btn-primary" type="button" onClick={openCreate}>
            <Plus size={16} /> 注册服务器
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <SummaryTile icon={Server} label="节点总数" value={summary.total} tone="brand" />
        <SummaryTile icon={CheckCircle2} label="健康节点" value={summary.healthy} tone="success" />
        <SummaryTile icon={AlertTriangle} label="降级节点" value={summary.degraded} tone="warning" />
        <SummaryTile icon={Activity} label="缩容中" value={summary.draining} tone="info" />
        <SummaryTile icon={CircleOff} label="离线节点" value={summary.down} tone="danger" />
      </div>

      <section className="section data-section">
        <div className="panel-header">
          <div>
            <h3>节点列表</h3>
            <div className="panel-caption">负载、心跳和探测结果在同一行内完成判断。</div>
          </div>
        </div>
        <div className="table-wrap">
          <table className="data-table server-table">
            <thead>
              <tr>
                <th>节点</th>
                <th>API</th>
                <th>关联域名</th>
                <th>负载</th>
                <th>状态</th>
                <th>心跳</th>
                <th>探测</th>
                <th>失败</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {servers.map(server => {
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
                          : <span className="muted-text">未绑定</span>}
                      </div>
                    </td>
                    <td>
                      <div className="load-cell">
                        <div className="load-meta">
                          <span>{server.current_load || 0} / {server.capacity || 0}</span>
                          <span>{percent}%</span>
                        </div>
                        <div className="progress" aria-label={`${server.name} 负载 ${percent}%`}>
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
                    <td>{formatDate(server.last_heartbeat)}</td>
                    <td>{formatDate(server.last_probe_at)}</td>
                    <td>
                      <span className={(server.probe_fail_count || 0) > 0 ? 'tag tag-warning' : 'tag tag-success'}>
                        {server.probe_fail_count || 0}
                      </span>
                    </td>
                    <td>
                      <div className="row-actions">
                        <button className="icon-button compact" type="button" title="编辑" onClick={() => openEdit(server)}>
                          <Pencil size={15} />
                        </button>
                        <button className="icon-button compact" type="button" title={server.status === 'draining' ? '恢复服务' : '缩容'} onClick={() => toggleStatus(server)}>
                          {server.status === 'draining' ? <Power size={15} /> : <Activity size={15} />}
                        </button>
                        <button className="icon-button compact danger" type="button" title="删除" onClick={() => askDelete(server)}>
                          <Trash2 size={15} />
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
              {servers.length === 0 && (
                <tr>
                  <td colSpan={9}>
                    <div className="empty-state">
                      <Database size={28} />
                      <strong>暂无服务器节点</strong>
                      <span>注册第一台 mail-node 后，MailHub 才能开始分配邮箱。</span>
                      <button className="btn btn-primary" type="button" onClick={openCreate}>
                        <Plus size={16} /> 注册服务器
                      </button>
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
