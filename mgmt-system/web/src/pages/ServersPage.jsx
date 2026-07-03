import { useState, useEffect, useCallback } from 'react'
import { serverAPI } from '../api'

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])
  return <div className={`toast toast-${type}`}>{message}</div>
}

function ConfirmDialog({ title, message, onConfirm, onCancel }) {
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal" style={{ width: 420 }} onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p style={{ color: '#666', fontSize: 14, marginBottom: 16 }}>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" onClick={onCancel}>取消</button>
          <button className="btn btn-danger" onClick={onConfirm}>确认</button>
        </div>
      </div>
    </div>
  )
}

export default function ServersPage() {
  const [servers, setServers] = useState([])
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [editing, setEditing] = useState(null)
  const [showAdd, setShowAdd] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    serverAPI.list()
      .then(data => setServers(Array.isArray(data) ? data : []))
      .catch(e => setToast({ type: 'error', message: '加载失败: ' + e.message }))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  // ── Add ──
  const handleAdd = async (e) => {
    e.preventDefault()
    const fd = new FormData(e.target)
    const data = {
      name: fd.get('name'),
      api_host: fd.get('api_host'),
      capacity: parseInt(fd.get('capacity')) || 5000,
    }
    setSaving(true)
    try {
      await serverAPI.create(data)
      setShowAdd(false)
      e.target.reset()
      setToast({ type: 'success', message: '✅ 服务器注册成功' })
      load()
    } catch (err) {
      setToast({ type: 'error', message: '❌ ' + err.message })
    } finally {
      setSaving(false)
    }
  }

  // ── Edit ──
  const openEdit = (s) => setEditing({
    id: s.id, name: s.name, api_host: s.api_host, smtp_host: s.smtp_host,
    imap_host: s.imap_host, capacity: s.capacity, heartbeat_interval: s.heartbeat_interval || 30,
    status: s.status,
  })

  const handleEdit = async (e) => {
    e.preventDefault()
    setSaving(true)
    try {
      await serverAPI.update(editing.id, editing)
      setEditing(null)
      setToast({ type: 'success', message: '✅ 修改保存成功' })
      load()
    } catch (err) {
      setToast({ type: 'error', message: '❌ ' + err.message })
    } finally {
      setSaving(false)
    }
  }

  // ── Status toggle ──
  const toggleStatus = (s) => {
    const newStatus = s.status === 'draining' ? 'healthy' : 'draining'
    const verb = newStatus === 'draining' ? '缩容（不再分配新邮箱）' : '恢复服务（重新参与分配）'
    setConfirm({
      title: '变更服务器状态',
      message: `确定将「${s.name}」${verb} 吗？`,
      onConfirm: async () => {
        try {
          await serverAPI.update(s.id, { status: newStatus })
          setToast({ type: 'success', message: '✅ 状态已更新' })
          load()
        } catch (err) {
          setToast({ type: 'error', message: '❌ ' + err.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  // ── Delete ──
  const handleDelete = (s) => {
    setConfirm({
      title: '删除服务器',
      message: `确定要删除「${s.name}」吗？此操作不可撤销。`,
      onConfirm: async () => {
        try {
          await serverAPI.remove(s.id)
          setToast({ type: 'success', message: '✅ 服务器已删除' })
          load()
        } catch (err) {
          setToast({ type: 'error', message: '❌ ' + err.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  if (loading) return <div style={{ textAlign: 'center', paddingTop: 80, color: '#888' }}>加载中...</div>

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <h1>服务器管理</h1>
        <button className="btn btn-primary" onClick={() => setShowAdd(!showAdd)}>
          ➕ 注册服务器
        </button>
      </div>

      {/* Add form */}
      {showAdd && (
        <div className="section" style={{ marginBottom: 20 }}>
          <h3>注册新服务器</h3>
          <form onSubmit={handleAdd}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <div className="form-group">
                <label>服务器名称 *</label>
                <input name="name" placeholder="例如: mail-node-01" required />
              </div>
              <div className="form-group">
                <label>API 地址 *</label>
                <input name="api_host" placeholder="例如: 10.0.0.2:8081" required />
              </div>
            </div>
            <p style={{ fontSize: 12, color: '#888', marginTop: -8, marginBottom: 14 }}>
              SMTP / IMAP 地址将根据 API 地址自动推导，如需单独指定请在注册后编辑。
            </p>
            <div className="form-group">
              <label>容量上限</label>
              <input name="capacity" type="number" defaultValue={5000} min={1} />
            </div>
            <button className="btn btn-success" type="submit" disabled={saving}>
              {saving && <span className="spinner" />} 注册
            </button>
            <button className="btn btn-outline" type="button" style={{ marginLeft: 8 }} onClick={() => setShowAdd(false)}>
              取消
            </button>
          </form>
        </div>
      )}

      {/* Server list */}
      <div className="section">
        <table>
          <thead>
            <tr>
              <th>ID</th><th>名称</th><th>API</th><th>关联域名</th><th>负载</th><th>状态</th><th>心跳</th><th>探测</th><th>失败</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {servers.map(s => (
              <tr key={s.id}>
                <td>{s.id}</td>
                <td><strong>{s.name}</strong></td>
                <td><code>{s.api_host}</code></td>
                <td>
                  {s.domains && s.domains.length > 0
                    ? s.domains.map(d => <span key={d.id} className="tag tag-info" style={{ marginRight: 4 }}>{d.name}</span>)
                    : '-'}
                </td>
                <td>{s.current_load} / {s.capacity}</td>
                <td><span className={`tag tag-${s.status === 'healthy' ? 'success' : s.status === 'degraded' ? 'warning' : s.status === 'draining' ? 'info' : 'danger'}`}>{s.status}</span></td>
                <td>{s.last_heartbeat ? new Date(s.last_heartbeat).toLocaleString('zh-CN', { hour12: false }) : '-'}</td>
                <td>{s.last_probe_at ? new Date(s.last_probe_at).toLocaleString('zh-CN', { hour12: false }) : '-'}</td>
                <td>{s.probe_fail_count || 0}</td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  <button className="btn btn-sm btn-primary" onClick={() => openEdit(s)}>编辑</button>
                  {s.status === 'draining' ? (
                    <button className="btn btn-sm btn-success" style={{ marginLeft: 4 }} onClick={() => toggleStatus(s)}>恢复服务</button>
                  ) : (
                    <button className="btn btn-sm" style={{ marginLeft: 4, background: '#6c757d', color: '#fff' }} onClick={() => toggleStatus(s)}>缩容</button>
                  )}
                  <button className="btn btn-sm btn-danger" style={{ marginLeft: 4 }} onClick={() => handleDelete(s)}>删除</button>
                </td>
              </tr>
            ))}
            {servers.length === 0 && (
              <tr><td colSpan={10} style={{ textAlign: 'center', color: '#888', padding: 20 }}>暂无服务器，请先注册</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Edit modal */}
      {editing && (
        <div className="modal-overlay" onClick={() => setEditing(null)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <h3>编辑服务器 #{editing.id}</h3>
            <form onSubmit={handleEdit}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <div className="form-group">
                  <label>名称</label>
                  <input value={editing.name} onChange={e => setEditing({ ...editing, name: e.target.value })} />
                </div>
                <div className="form-group">
                  <label>API 地址</label>
                  <input value={editing.api_host} onChange={e => setEditing({ ...editing, api_host: e.target.value })} />
                </div>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <div className="form-group">
                  <label>SMTP 地址</label>
                  <input value={editing.smtp_host || ''} onChange={e => setEditing({ ...editing, smtp_host: e.target.value })} placeholder="留空则从 API 地址推导" />
                </div>
                <div className="form-group">
                  <label>IMAP 地址</label>
                  <input value={editing.imap_host || ''} onChange={e => setEditing({ ...editing, imap_host: e.target.value })} placeholder="留空则从 API 地址推导" />
                </div>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <div className="form-group">
                  <label>容量</label>
                  <input type="number" value={editing.capacity} onChange={e => setEditing({ ...editing, capacity: parseInt(e.target.value) || 0 })} />
                </div>
                <div className="form-group">
                  <label>心跳间隔 (秒, 5-600)</label>
                  <input type="number" min={5} max={600} value={editing.heartbeat_interval} onChange={e => setEditing({ ...editing, heartbeat_interval: parseInt(e.target.value) || 0 })} />
                </div>
              </div>
              <div className="form-group">
                <label>状态</label>
                <select value={editing.status} onChange={e => setEditing({ ...editing, status: e.target.value })}>
                  <option value="healthy">healthy</option>
                  <option value="degraded">degraded</option>
                  <option value="draining">draining</option>
                  <option value="down">down</option>
                </select>
              </div>
              <div className="modal-footer">
                <button className="btn btn-danger" type="button" onClick={() => handleDelete({ id: editing.id, name: editing.name })}>删除</button>
                <button className="btn btn-outline" type="button" onClick={() => setEditing(null)}>取消</button>
                <button className="btn btn-primary" type="submit" disabled={saving}>
                  {saving && <span className="spinner" />} 保存
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
