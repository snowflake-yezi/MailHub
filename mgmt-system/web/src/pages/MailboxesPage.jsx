import { useState, useEffect, useCallback } from 'react'
import { mailboxAPI, serverAPI, domainAPI } from '../api'

const STATUS_TAG = {
  active: 'tag-success',
  synced: 'tag-success',
  pending: 'tag-warning',
  deleting: 'tag-warning',
  disabled: 'tag-danger',
  recycled: 'tag-danger',
  sync_failed: 'tag-danger',
  soft_deleted: 'tag-info',
  purged: 'tag-info',
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])
  return <div className={`toast toast-${type}`}>{message}</div>
}

export default function MailboxesPage() {
  const [view, setView] = useState('normal') // 'normal' | 'trash' | 'create'
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState(null)
  const [servers, setServers] = useState([])
  const [domains, setDomains] = useState([])

  // Filters
  const [search, setSearch] = useState('')
  const [domainId, setDomainId] = useState('')
  const [serverId, setServerId] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [page, setPage] = useState(1)

  // Create form
  const [createPrefix, setCreatePrefix] = useState('')
  const [createPassword, setCreatePassword] = useState('')
  const [createServerId, setCreateServerId] = useState('0')
  const [batchText, setBatchText] = useState('')
  const [createTab, setCreateTab] = useState('batch')
  const [creating, setCreating] = useState(false)
  const [batchResult, setBatchResult] = useState(null)

  // Password edit
  const [pwdModal, setPwdModal] = useState(null)
  const [pwdSaving, setPwdSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const params = { page, search, domain_id: domainId, server_id: serverId }
      if (view === 'trash') {
        params.view = 'trash'
      } else if (statusFilter) {
        params.status = statusFilter
      }
      const data = await mailboxAPI.list(params)
      setItems(Array.isArray(data?.items) ? data.items : Array.isArray(data) ? data : [])
      setTotal(data?.total_count ?? data?.total ?? 0)
    } catch (e) {
      setToast({ type: 'error', message: '加载失败: ' + e.message })
    } finally {
      setLoading(false)
    }
  }, [view, search, domainId, serverId, statusFilter, page])

  useEffect(() => {
    load()
    // Load dropdowns
    serverAPI.list().then(d => setServers(Array.isArray(d) ? d : [])).catch(() => {})
    domainAPI.list().then(d => setDomains(Array.isArray(d) ? d : [])).catch(() => {})
  }, [load])

  const handleSearch = (e) => {
    e.preventDefault()
    setPage(1)
    load()
  }

  const handleCreate = async (e) => {
    e.preventDefault()
    const items = createTab === 'batch'
      ? batchText.split('\n').filter(Boolean).map(line => {
          const [prefix, ...rest] = line.split(',')
          return { prefix: prefix.trim(), password: rest.join(',').trim() }
        })
      : [{ prefix: createPrefix.trim(), password: createPassword.trim(), server_id: parseInt(createServerId) || 0 }]

    if (items.length === 0) return
    setCreating(true)
    setBatchResult(null)
    try {
      const result = await mailboxAPI.batchCreate(items)
      setBatchResult(result)
      setToast({ type: 'success', message: `创建完成: 成功 ${result.success || 0}, 失败 ${result.failed || 0}` })
      setView('normal')
      load()
    } catch (e) {
      setToast({ type: 'error', message: '创建失败: ' + e.message })
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async (item) => {
    if (!confirm(`确认删除邮箱 ${item.email_address}？\n\n这将摘除 Postfix 收信、等待转发排空后将 Maildir 移入回收站。`)) return
    try {
      await mailboxAPI.remove(item.id)
      setToast({ type: 'success', message: '已提交删除: ' + item.email_address })
      load()
    } catch (e) {
      setToast({ type: 'error', message: '删除失败: ' + e.message })
    }
  }

  const handleRestore = async (item) => {
    if (!confirm(`确认恢复邮箱 ${item.email_address}？\n\n将从回收站回迁 Maildir，重建配置。仅删除后 24h 内可恢复。`)) return
    try {
      const data = await mailboxAPI.restore(item.id)
      let msg = '已恢复: ' + item.email_address
      if (data?.password) msg += '\n\n⚠️ 原密码未知，已生成临时密码：' + data.password + '\n请立即保存。'
      alert(msg)
      load()
    } catch (e) {
      setToast({ type: 'error', message: '恢复失败: ' + e.message })
    }
  }

  const handlePurge = async (item) => {
    if (!confirm(`确认彻底删除邮箱 ${item.email_address}？\n\n此操作不可恢复：将立即永久清除。`)) return
    try {
      await mailboxAPI.purge(item.id)
      setToast({ type: 'success', message: '已彻底删除: ' + item.email_address })
      load()
    } catch (e) {
      setToast({ type: 'error', message: '操作失败: ' + e.message })
    }
  }

  const openPwdModal = (item) => setPwdModal({ id: item.id, email: item.email_address, password: '' })
  const handlePwdSave = async () => {
    if (!pwdModal.password || pwdModal.password.length < 6) {
      setToast({ type: 'error', message: '密码至少6个字符' })
      return
    }
    setPwdSaving(true)
    try {
      await mailboxAPI.updatePassword(pwdModal.id, pwdModal.password)
      setPwdModal(null)
      setToast({ type: 'success', message: '密码修改成功' })
      load()
    } catch (e) {
      setToast({ type: 'error', message: '修改失败: ' + e.message })
    } finally {
      setPwdSaving(false)
    }
  }

  const copyText = (text) => {
    navigator.clipboard.writeText(text).then(() => {
      setToast({ type: 'success', message: '已复制' })
    })
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20 }}>
        <div>
          <h1>邮箱账号台账</h1>
          <p style={{ color: '#888', fontSize: 13, marginTop: 4 }}>以服务器、域名、邮箱账密为主维度；订单通过映射表关联账号。</p>
        </div>
        <button className="btn btn-primary" onClick={() => { setView('create'); setBatchResult(null) }}>创建邮箱</button>
      </div>

      {/* Summary */}
      <div className="cards" style={{ gridTemplateColumns: 'repeat(3, 1fr)' }}>
        <div className="card">
          <div className="value">{total}</div>
          <div className="label">筛选结果</div>
        </div>
        <div className="card">
          <div className="value">{domainId ? domains.find(d => d.id === parseInt(domainId))?.name || `#${domainId}` : '全部'}</div>
          <div className="label">域名筛选</div>
        </div>
        <div className="card">
          <div className="value">{serverId ? servers.find(s => s.id === parseInt(serverId))?.name || `#${serverId}` : '全部'}</div>
          <div className="label">服务器筛选</div>
        </div>
      </div>

      {/* Tabs */}
      <div className="tabs" style={{ display: 'inline-flex', gap: 2, padding: 3, background: '#e9ecef', borderRadius: 8, marginBottom: 16 }}>
        <button className={`btn btn-sm ${view === 'normal' ? 'btn-primary' : 'btn-outline'}`} style={{ borderRadius: 6 }} onClick={() => { setView('normal'); setPage(1) }}>账号集合</button>
        <button className={`btn btn-sm ${view === 'trash' ? 'btn-primary' : 'btn-outline'}`} style={{ borderRadius: 6 }} onClick={() => { setView('trash'); setPage(1) }}>回收站</button>
        <button className={`btn btn-sm ${view === 'create' ? 'btn-primary' : 'btn-outline'}`} style={{ borderRadius: 6 }} onClick={() => { setView('create'); setBatchResult(null) }}>创建邮箱</button>
      </div>

      {/* ── List View ── */}
      {(view === 'normal' || view === 'trash') && (
        <div className="section">
          {view === 'trash' && (
            <div style={{ background: '#fff7df', color: '#8a5a00', border: '1px solid #f0d8a0', borderRadius: 8, padding: '10px 14px', fontSize: 13, marginBottom: 16 }}>
              回收站保留删除后 <strong>24 小时</strong> 内可恢复的邮箱；超过 24h 远端已物理清除，恢复将失败。
              「彻底删除」会立即转为已清除状态，<strong>不可恢复</strong>。
            </div>
          )}

          <form onSubmit={handleSearch} style={{ display: 'grid', gridTemplateColumns: '1fr 160px 180px 150px auto auto', gap: 8, padding: '14px 16px', borderBottom: '1px solid #eee' }}>
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索邮箱地址或前缀" />
            <select value={domainId} onChange={e => setDomainId(e.target.value)}>
              <option value="">全部域名</option>
              {domains.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
            <select value={serverId} onChange={e => setServerId(e.target.value)}>
              <option value="">全部服务器</option>
              {servers.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
            {view === 'normal' && (
              <select value={statusFilter} onChange={e => setStatusFilter(e.target.value)}>
                <option value="">全部状态</option>
                <option value="active">active</option>
                <option value="deleting">deleting</option>
              </select>
            )}
            <button className="btn btn-primary btn-sm" type="submit">筛选</button>
            <button className="btn btn-outline btn-sm" type="button" onClick={() => { setSearch(''); setDomainId(''); setServerId(''); setStatusFilter(''); setPage(1); }}>重置</button>
          </form>

          <div style={{ overflowX: 'auto' }}>
            <table>
              <thead>
                <tr>
                  <th>账号ID</th>
                  <th>邮箱地址</th>
                  {view === 'normal' && <th>密码</th>}
                  <th>域名</th>
                  <th>服务器</th>
                  <th>状态</th>
                  {view === 'normal' && <th>同步</th>}
                  {view === 'trash' && <th>删除时间</th>}
                  {view === 'normal' && <th>创建时间</th>}
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map(item => (
                  <tr key={item.id}>
                    <td><code style={{ fontSize: 12 }}>#{item.id}</code></td>
                    <td>
                      <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{item.email_address}</span>
                      <button className="btn btn-sm btn-outline" style={{ marginLeft: 4, padding: '0 6px', height: 22 }} onClick={() => copyText(item.email_address)}>⧉</button>
                    </td>
                    {view === 'normal' && (
                      <td>
                        {item.password ? (
                          <>
                            <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{item.password}</span>
                            <button className="btn btn-sm btn-outline" style={{ marginLeft: 4, padding: '0 6px', height: 22 }} onClick={() => copyText(item.password)}>⧉</button>
                          </>
                        ) : <span style={{ color: '#888' }}>未记录</span>}
                      </td>
                    )}
                    <td>{item.domain?.name || `#${item.domain_id}`}</td>
                    <td>{item.server?.name || `#${item.server_id}`}</td>
                    <td><span className={`tag ${STATUS_TAG[item.status] || ''}`}>{item.status}</span></td>
                    {view === 'normal' && <td>{item.sync_status ? <span className={`tag ${STATUS_TAG[item.sync_status] || ''}`}>{item.sync_status}</span> : <span style={{ color: '#888' }}>-</span>}</td>}
                    {view === 'trash' && <td>{item.recycled_at ? new Date(item.recycled_at).toLocaleString('zh-CN', { hour12: false }) : '-'}</td>}
                    {view === 'normal' && <td>{item.created_at ? new Date(item.created_at).toLocaleString('zh-CN', { hour12: false }) : '-'}</td>}
                    <td style={{ whiteSpace: 'nowrap' }}>
                      {view === 'normal' && (
                        <>
                          <button className="btn btn-sm btn-primary" onClick={() => openPwdModal(item)}>改密</button>
                          {(item.status === 'active' || item.status === 'deleting') && (
                            <button className="btn btn-sm btn-outline" style={{ marginLeft: 4 }} onClick={() => handleDelete(item)}>删除</button>
                          )}
                          {item.status === 'soft_deleted' && (
                            <button className="btn btn-sm btn-primary" style={{ marginLeft: 4 }} onClick={() => handleRestore(item)}>恢复</button>
                          )}
                        </>
                      )}
                      {view === 'trash' && item.status === 'soft_deleted' && (
                        <>
                          <button className="btn btn-sm btn-primary" onClick={() => handleRestore(item)}>恢复</button>
                          <button className="btn btn-sm btn-danger" style={{ marginLeft: 4 }} onClick={() => handlePurge(item)}>彻底删除</button>
                        </>
                      )}
                      {view === 'trash' && item.status === 'purged' && (
                        <span style={{ color: '#888', fontSize: 12 }}>已彻底清除</span>
                      )}
                    </td>
                  </tr>
                ))}
                {items.length === 0 && (
                  <tr><td colSpan={view === 'normal' ? 9 : 7} style={{ textAlign: 'center', color: '#888', padding: 20 }}>
                    {view === 'trash' ? '回收站为空' : '暂无邮箱账号'}
                  </td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* ── Create View ── */}
      {view === 'create' && (
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(300px, 420px) minmax(320px, 1fr)', gap: 16 }}>
          <div className="section">
            <h3>单个创建</h3>
            <form onSubmit={handleCreate}>
              <div className="form-group">
                <label>邮箱前缀</label>
                <input value={createPrefix} onChange={e => setCreatePrefix(e.target.value)} placeholder="airline-cz-001" required />
              </div>
              <div className="form-group">
                <label>密码</label>
                <input value={createPassword} onChange={e => setCreatePassword(e.target.value)} placeholder="留空自动生成" />
              </div>
              <div className="form-group">
                <label>目标服务器</label>
                <select value={createServerId} onChange={e => setCreateServerId(e.target.value)}>
                  <option value="0">自动选择</option>
                  {servers.map(s => <option key={s.id} value={s.id}>{s.name} ({s.status}, {s.current_load}/{s.capacity})</option>)}
                </select>
              </div>
              <button className="btn btn-primary" type="submit" disabled={creating} onClick={() => setCreateTab('single')}>
                {creating && <span className="spinner" />} 创建邮箱
              </button>
            </form>
          </div>

          <div className="section">
            <h3>批量创建</h3>
            <div style={{ display: 'flex', gap: 4, marginBottom: 12 }}>
              <button className={`btn btn-sm ${createTab === 'batch' ? 'btn-primary' : 'btn-outline'}`} onClick={() => setCreateTab('batch')}>批量粘贴</button>
              <button className={`btn btn-sm ${createTab === 'csv' ? 'btn-primary' : 'btn-outline'}`} onClick={() => setCreateTab('csv')}>CSV 上传</button>
            </div>

            {createTab === 'batch' && (
              <form onSubmit={handleCreate}>
                <p style={{ color: '#888', fontSize: 12, marginBottom: 10 }}>每行一个邮箱前缀，格式：<code>前缀</code> 或 <code>前缀,密码</code></p>
                <div className="form-group">
                  <label>邮箱列表</label>
                  <textarea value={batchText} onChange={e => setBatchText(e.target.value)} rows={8} placeholder="airline-cz-001&#10;airline-cz-002&#10;airline-cz-003,mypassword123" />
                </div>
                <button className="btn btn-primary" type="submit" disabled={creating || !batchText.trim()}>
                  {creating && <span className="spinner" />} 批量创建
                </button>
              </form>
            )}

            {createTab === 'csv' && (
              <form onSubmit={e => { e.preventDefault(); setToast({ type: 'error', message: 'CSV 上传请使用 API: POST /api/v1/admin/mailboxes/upload' }) }}>
                <div className="form-group">
                  <label>选择文件</label>
                  <input type="file" accept=".csv,.txt" />
                </div>
                <button className="btn btn-primary" type="submit">上传并创建</button>
              </form>
            )}
          </div>

          {batchResult && (
            <div className="section" style={{ gridColumn: '1 / -1' }}>
              <h3>创建结果 — 成功 {batchResult.success || 0}，失败 {batchResult.failed || 0}</h3>
              {(batchResult.results || []).map((r, i) => (
                <div key={i} style={{ padding: '8px 0', borderBottom: '1px solid #eee', fontSize: 13 }}>
                  <strong>{r.email_address || r.prefix}</strong>{' '}
                  {r.password && <code style={{ fontSize: 12 }}>{r.password}</code>}{' '}
                  <span className={`tag ${r.status === 'success' ? 'tag-success' : 'tag-danger'}`}>{r.status}</span>
                  {r.error && <div style={{ color: '#a82424', fontSize: 12, marginTop: 4 }}>{r.error}</div>}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Password modal */}
      {pwdModal && (
        <div className="modal-overlay" onClick={() => setPwdModal(null)}>
          <div className="modal" style={{ width: 420 }} onClick={e => e.stopPropagation()}>
            <h3>修改邮箱密码</h3>
            <div className="form-group">
              <label>邮箱地址</label>
              <input value={pwdModal.email} readOnly style={{ background: '#f5f5f5' }} />
            </div>
            <div className="form-group">
              <label>新密码（至少6个字符）</label>
              <input value={pwdModal.password} onChange={e => setPwdModal({ ...pwdModal, password: e.target.value })} placeholder="输入新密码" autoFocus />
            </div>
            <div className="modal-footer">
              <button className="btn btn-outline" onClick={() => setPwdModal(null)}>取消</button>
              <button className="btn btn-primary" onClick={handlePwdSave} disabled={pwdSaving}>
                {pwdSaving && <span className="spinner" />} 保存
              </button>
            </div>
          </div>
        </div>
      )}

      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
