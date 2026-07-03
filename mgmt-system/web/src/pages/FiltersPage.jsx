import { useState, useEffect, useCallback } from 'react'
import { filterAPI } from '../api'

const RULE_TYPES = [
  { value: 'whitelist_sender', label: '发件人白名单' },
  { value: 'blacklist_sender', label: '发件人黑名单' },
  { value: 'keyword', label: '关键词' },
  { value: 'regex', label: '正则表达式' },
]

const ACTIONS = [
  { value: 'pass', label: 'pass（放行转发）' },
  { value: 'flag', label: 'flag（标记疑似）' },
  { value: 'block', label: 'block（直接丢弃）' },
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

export default function FiltersPage() {
  const [rules, setRules] = useState([])
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState(null)
  const [modal, setModal] = useState(null) // { mode: 'add'|'edit', data }
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    filterAPI.list()
      .then(data => setRules(Array.isArray(data) ? data : []))
      .catch(e => setToast({ type: 'error', message: '加载失败: ' + e.message }))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const openAdd = () => setModal({
    mode: 'add',
    data: { name: '', rule_type: 'whitelist_sender', pattern: '', action: 'pass', priority: 10, enabled: true },
  })

  const openEdit = (r) => setModal({ mode: 'edit', data: { ...r } })

  const handleSave = async (e) => {
    e.preventDefault()
    setSaving(true)
    try {
      if (modal.mode === 'add') {
        await filterAPI.create(modal.data)
      } else {
        await filterAPI.update(modal.data.id, modal.data)
      }
      setModal(null)
      setToast({ type: 'success', message: '✅ 保存成功' })
      load()
    } catch (err) {
      setToast({ type: 'error', message: '❌ ' + err.message })
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (r) => {
    if (!confirm(`确定删除规则「${r.name}」？`)) return
    try {
      await filterAPI.remove(r.id)
      setToast({ type: 'success', message: '✅ 已删除' })
      load()
    } catch (err) {
      setToast({ type: 'error', message: '❌ ' + err.message })
    }
  }

  if (loading) return <div style={{ textAlign: 'center', paddingTop: 80, color: '#888' }}>加载中...</div>

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20 }}>
        <div>
          <h1>过滤规则管理</h1>
          <p style={{ color: '#888', fontSize: 13, marginTop: 4 }}>规则优先级数字越小越先匹配。修改后邮箱服务器 30 秒内自动拉取生效。</p>
        </div>
        <button className="btn btn-primary" onClick={openAdd}>➕ 新增规则</button>
      </div>

      <div className="section">
        <table>
          <thead>
            <tr>
              <th>优先级</th><th>名称</th><th>类型</th><th>匹配模式</th><th>动作</th><th>启用</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {rules.map(r => (
              <tr key={r.id}>
                <td>{r.priority}</td>
                <td><strong>{r.name}</strong></td>
                <td><span className={`tag ${TYPE_TAG[r.rule_type] || ''}`}>{r.rule_type}</span></td>
                <td><code>{r.pattern}</code></td>
                <td><span className={`tag ${ACTION_TAG[r.action] || ''}`}>{r.action}</span></td>
                <td>{r.enabled ? '✅' : '❌'}</td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  <button className="btn btn-sm btn-primary" onClick={() => openEdit(r)}>编辑</button>
                  <button className="btn btn-sm btn-danger" style={{ marginLeft: 4 }} onClick={() => handleDelete(r)}>删除</button>
                </td>
              </tr>
            ))}
            {rules.length === 0 && (
              <tr><td colSpan={7} style={{ textAlign: 'center', color: '#888', padding: 20 }}>暂无过滤规则，去添加第一条吧</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Modal */}
      {modal && (
        <div className="modal-overlay" onClick={() => setModal(null)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <h3>{modal.mode === 'add' ? '新增规则' : `编辑规则 #${modal.data.id}`}</h3>
            <form onSubmit={handleSave}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <div className="form-group">
                  <label>规则名称 *</label>
                  <input value={modal.data.name} onChange={e => setModal({ ...modal, data: { ...modal.data, name: e.target.value } })} placeholder="例如: 白名单-XX航空" required />
                </div>
                <div className="form-group">
                  <label>类型 *</label>
                  <select value={modal.data.rule_type} onChange={e => setModal({ ...modal, data: { ...modal.data, rule_type: e.target.value } })}>
                    {RULE_TYPES.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
                  </select>
                </div>
              </div>
              <div className="form-group">
                <label>匹配模式 *</label>
                <input value={modal.data.pattern} onChange={e => setModal({ ...modal, data: { ...modal.data, pattern: e.target.value } })} placeholder="白名单/黑名单: @airline.com — 关键词: 行程单 — 正则: (?i)itinerary" required />
                <div className="form-hint">白名单/黑名单填写发件人域名; 关键词填写要匹配的词; 正则填写 Go 正则表达式</div>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                <div className="form-group">
                  <label>动作</label>
                  <select value={modal.data.action} onChange={e => setModal({ ...modal, data: { ...modal.data, action: e.target.value } })}>
                    {ACTIONS.map(a => <option key={a.value} value={a.value}>{a.label}</option>)}
                  </select>
                </div>
                <div className="form-group">
                  <label>优先级</label>
                  <input type="number" value={modal.data.priority} onChange={e => setModal({ ...modal, data: { ...modal.data, priority: parseInt(e.target.value) || 0 } })} min={0} />
                </div>
              </div>
              <div className="form-group">
                <label className="toggle" style={{ display: 'inline-flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
                  <input type="checkbox" checked={modal.data.enabled} onChange={e => setModal({ ...modal, data: { ...modal.data, enabled: e.target.checked } })} />
                  <span className="toggle-slider" />
                  <span style={{ fontSize: 13, color: '#666' }}>启用此规则</span>
                </label>
              </div>
              <div className="modal-footer">
                <button className="btn btn-outline" type="button" onClick={() => setModal(null)}>取消</button>
                <button className="btn btn-success" type="submit" disabled={saving}>
                  {saving && <span className="spinner" />} 保存
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
