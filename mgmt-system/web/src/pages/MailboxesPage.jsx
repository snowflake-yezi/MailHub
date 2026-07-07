import { useState, useEffect, useCallback } from 'react'
import { mailboxAPI, serverAPI, domainAPI, integratedMailboxAPI } from '../api'

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
  ok: 'tag-success',
  fail: 'tag-danger',
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])
  return <div className={`toast toast-${type}`}>{message}</div>
}

function csvEscape(value) {
  const text = value === undefined || value === null ? '' : String(value)
  if (/[",\n\r]/.test(text)) return `"${text.replace(/"/g, '""')}"`
  return text
}

function downloadCsv(filename, rows) {
  const content = rows.map(row => row.map(csvEscape).join(',')).join('\n')
  const blob = new Blob(['﻿' + content], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

export default function MailboxesPage() {
  const [view, setView] = useState('normal') // 'normal' | 'trash' | 'integrated'
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
  const [size, setSize] = useState(20)

  // Create form
  const [createPrefix, setCreatePrefix] = useState('')
  const [createPassword, setCreatePassword] = useState('')
  const [createServerId, setCreateServerId] = useState('0')
  const [createDomainId, setCreateDomainId] = useState('0')
  const [batchText, setBatchText] = useState('')
  const [createTab, setCreateTab] = useState('single')
  const [creating, setCreating] = useState(false)
  const [batchResult, setBatchResult] = useState(null)

  // Password edit
  const [pwdModal, setPwdModal] = useState(null)
  const [pwdSaving, setPwdSaving] = useState(false)

  // Integrated mailboxes (forward target pool)
  const [integrated, setIntegrated] = useState([])
  const [integratedModal, setIntegratedModal] = useState(null) // { mode: 'add'|'edit', data }
  const [integratedSaving, setIntegratedSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const params = { page, size, search, domain_id: domainId, server_id: serverId }
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
  }, [view, search, domainId, serverId, statusFilter, page, size])

  useEffect(() => {
    load()
    // Load dropdowns
    serverAPI.list().then(d => setServers(Array.isArray(d) ? d : [])).catch(() => {})
    domainAPI.list().then(d => setDomains(Array.isArray(d) ? d : [])).catch(() => {})
  }, [load])

  const loadIntegrated = useCallback(() => {
    integratedMailboxAPI.list()
      .then(d => setIntegrated(Array.isArray(d) ? d : []))
      .catch(e => setToast({ type: 'error', message: '加载集成邮箱失败: ' + e.message }))
  }, [])

  useEffect(() => {
    if (view === 'integrated') loadIntegrated()
  }, [view, loadIntegrated])

  const handleSearch = (e) => {
    e.preventDefault()
    setPage(1)
    load()
  }

  const serverDomains = (server) => server?.domains || server?.Domains || []
  const selectedCreateServer = servers.find(s => s.id === parseInt(createServerId))
  const selectedCreateDomain = domains.find(d => d.id === parseInt(createDomainId))
  const domainIsServedByServer = (server, domainValue) => {
    if (!server || !domainValue || domainValue === '0') return true
    return serverDomains(server).some(d => d.id === parseInt(domainValue))
  }
  const createServerOptions = createDomainId !== '0'
    ? servers.filter(s => domainIsServedByServer(s, createDomainId))
    : servers
  const createDomainOptions = createServerId !== '0' && selectedCreateServer
    ? serverDomains(selectedCreateServer)
    : domains

  const handleCreateServerChange = (value) => {
    setCreateServerId(value)
    const server = servers.find(s => s.id === parseInt(value))
    if (value !== '0' && createDomainId !== '0' && !domainIsServedByServer(server, createDomainId)) {
      setCreateDomainId('0')
      setToast({ type: 'error', message: '已清空域名：该服务器未绑定当前域名' })
    }
  }

  const handleCreateDomainChange = (value) => {
    setCreateDomainId(value)
    if (value !== '0' && createServerId !== '0' && !domainIsServedByServer(selectedCreateServer, value)) {
      setCreateServerId('0')
      setToast({ type: 'error', message: '已清空服务器：当前域名未绑定该服务器' })
    }
  }

  const buildCreateItem = (prefix, password = '') => ({
    prefix: prefix.trim(),
    password: password.trim(),
    server_id: parseInt(createServerId) || 0,
    domain_id: parseInt(createDomainId) || 0,
  })

  const submitCreateItems = async (createItems) => {
    const validItems = createItems.filter(item => item.prefix)
    if (validItems.length === 0) return
    setCreating(true)
    setBatchResult(null)
    try {
      const result = await mailboxAPI.batchCreate(validItems)
      setBatchResult(result)
      setToast({ type: 'success', message: `创建完成: 成功 ${result.success || 0}, 失败 ${result.failed || 0}` })
      load()
    } catch (e) {
      setToast({ type: 'error', message: '创建失败: ' + e.message })
    } finally {
      setCreating(false)
    }
  }

  const handleSingleCreate = (e) => {
    e.preventDefault()
    setCreateTab('single')
    submitCreateItems([buildCreateItem(createPrefix, createPassword)])
  }

  const handleBatchCreate = (e) => {
    e.preventDefault()
    setCreateTab('batch')
    const createItems = batchText.split('\n').filter(Boolean).map(line => {
      const [prefix, ...rest] = line.split(',')
      return buildCreateItem(prefix, rest.join(','))
    })
    submitCreateItems(createItems)
  }

  const downloadCredentialCsv = () => {
    const rows = [
      ['email_address', 'password', 'prefix', 'domain', 'server_id', 'status', 'error'],
      ...(batchResult?.results || []).map(r => [r.email_address || '', r.password || '', r.prefix || '', r.domain || '', r.server_id || '', r.status || '', r.error || '']),
    ]
    const ts = new Date().toISOString().replace(/[-:T]/g, '').slice(0, 12)
    downloadCsv(`mailbox-credentials-${ts}.csv`, rows)
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
      <div style={{ marginBottom: 20 }}>
        <h1>邮箱账号台账</h1>
        <p style={{ color: '#888', fontSize: 13, marginTop: 4 }}>以服务器、域名、邮箱账密为主维度；订单通过映射表关联账号。</p>
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
        <button className={`btn btn-sm ${view === 'integrated' ? 'btn-primary' : 'btn-outline'}`} style={{ borderRadius: 6 }} onClick={() => { setView('integrated'); loadIntegrated() }}>集成邮箱</button>
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
                    {loading ? '加载中...' : view === 'trash' ? '回收站为空' : '暂无邮箱账号'}
                  </td></tr>
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          {total > 0 && (() => {
            const totalPages = Math.max(1, Math.ceil(total / size))
            const safePage = Math.min(page, totalPages)
            return (
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderTop: '1px solid #eee', flexWrap: 'wrap' }}>
                <button className="btn btn-sm btn-outline" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>上一页</button>
                <span style={{ fontSize: 13, color: '#666' }}>第 <strong>{safePage}</strong> / {totalPages} 页（共 {total} 条）</span>
                <button className="btn btn-sm btn-outline" disabled={safePage >= totalPages} onClick={() => setPage(safePage + 1)}>下一页</button>
                <span style={{ flex: 1 }} />
                <label style={{ fontSize: 13, color: '#666', display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                  每页
                  <select value={size} onChange={e => { setSize(parseInt(e.target.value)); setPage(1) }}>
                    <option value={20}>20</option>
                    <option value={50}>50</option>
                    <option value={100}>100</option>
                  </select>
                  条
                </label>
                <label style={{ fontSize: 13, color: '#666', display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                  跳至
                  <input type="number" min={1} max={totalPages} style={{ width: 60 }} placeholder={safePage} onKeyDown={e => { if (e.key === 'Enter') { const p = parseInt(e.target.value); if (p >= 1 && p <= totalPages) setPage(p); e.target.value = '' } }} />
                  页
                </label>
              </div>
            )
          })()}
        </div>
      )}

      {/* ── Integrated Mailboxes View ── */}
      {view === 'integrated' && (
        <div className="section">
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 16px', borderBottom: '1px solid #eee' }}>
            <div>
              <strong>集成邮箱（转发目标池）</strong>
              <div style={{ color: '#888', fontSize: 12, marginTop: 4 }}>所有非垃圾邮件汇总转发到这里。标「当前生效」的是正在使用的转发目标，切换后 mail-node 自动重载生效。</div>
            </div>
            <button className="btn btn-primary btn-sm" onClick={() => setIntegratedModal({ mode: 'add', data: { email_address: '', display_name: '' } })}>➕ 新增</button>
          </div>
          <div style={{ overflowX: 'auto' }}>
            <table>
              <thead>
                <tr><th>ID</th><th>邮箱地址</th><th>备注</th><th>当前生效</th><th>操作</th></tr>
              </thead>
              <tbody>
                {integrated.map(m => (
                  <tr key={m.id}>
                    <td><code style={{ fontSize: 12 }}>#{m.id}</code></td>
                    <td><span style={{ fontFamily: 'monospace', fontSize: 12 }}>{m.email_address}</span></td>
                    <td>{m.display_name || '-'}</td>
                    <td>{m.is_active ? <span className="tag tag-success">当前生效</span> : <span style={{ color: '#888' }}>—</span>}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      {!m.is_active && (
                        <button className="btn btn-sm btn-success" onClick={async () => {
                          try { await integratedMailboxAPI.activate(m.id); setToast({ type: 'success', message: '✅ 已设为当前转发目标' }); loadIntegrated() }
                          catch (e) { setToast({ type: 'error', message: '❌ ' + e.message }) }
                        }}>设为当前目标</button>
                      )}
                      <button className="btn btn-sm btn-primary" style={{ marginLeft: 4 }} onClick={() => setIntegratedModal({ mode: 'edit', data: { ...m } })}>编辑</button>
                      <button className="btn btn-sm btn-danger" style={{ marginLeft: 4 }} disabled={m.is_active} title={m.is_active ? '当前生效项不可删除，请先切换' : ''} onClick={async () => {
                        if (!confirm(`确认删除集成邮箱 ${m.email_address}？`)) return
                        try { await integratedMailboxAPI.remove(m.id); setToast({ type: 'success', message: '已删除' }); loadIntegrated() }
                        catch (e) { setToast({ type: 'error', message: '❌ ' + e.message }) }
                      }}>删除</button>
                    </td>
                  </tr>
                ))}
                {integrated.length === 0 && (
                  <tr><td colSpan={5} style={{ textAlign: 'center', color: '#888', padding: 20 }}>暂无集成邮箱</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* ── Create Section (always at bottom) ── */}
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(300px, 420px) minmax(320px, 1fr)', gap: 16, marginTop: 18 }}>
        <div className="section" style={{ gridColumn: '1 / -1' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16, paddingBottom: 12, borderBottom: '1px solid #eee', marginBottom: 14 }}>
            <div>
              <h3 style={{ marginBottom: 4 }}>创建邮箱</h3>
              <div style={{ color: '#888', fontSize: 12 }}>服务器和域名均可选；不选择时系统会按健康服务器和可用域名池自动负载分配。</div>
            </div>
            {(createServerId === '0' && createDomainId === '0') && <span className="tag tag-info">自动分配</span>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <div className="form-group">
              <label>邮箱服务器（可选）</label>
              <select value={createServerId} onChange={e => handleCreateServerChange(e.target.value)}>
                <option value="0">自动选择服务器</option>
                {createServerOptions.map(s => <option key={s.id} value={s.id}>{s.name} ({s.status}, {s.current_load}/{s.capacity})</option>)}
              </select>
              {createDomainId !== '0' && <div style={{ color: '#888', fontSize: 12, marginTop: 4 }}>已按域名 {selectedCreateDomain?.name || `#${createDomainId}`} 限制服务器范围。</div>}
            </div>
            <div className="form-group">
              <label>域名（可选）</label>
              <select value={createDomainId} onChange={e => handleCreateDomainChange(e.target.value)}>
                <option value="0">自动选择域名</option>
                {createDomainOptions.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
              {createServerId !== '0' && <div style={{ color: '#888', fontSize: 12, marginTop: 4 }}>已按服务器 {selectedCreateServer?.name || `#${createServerId}`} 限制域名范围。</div>}
            </div>
          </div>
        </div>

        <div className="section">
          <h3>单个创建</h3>
          <form onSubmit={handleSingleCreate}>
            <div className="form-group">
              <label>邮箱前缀</label>
              <input value={createPrefix} onChange={e => setCreatePrefix(e.target.value)} placeholder="airline-cz-001" required />
            </div>
            <div className="form-group">
              <label>密码</label>
              <input value={createPassword} onChange={e => setCreatePassword(e.target.value)} placeholder="留空自动生成" />
            </div>
            <button className="btn btn-primary" type="submit" disabled={creating || !createPrefix.trim()}>
              {creating && createTab === 'single' && <span className="spinner" />} 创建邮箱
            </button>
          </form>
        </div>

        <div className="section">
          <h3>批量创建</h3>
          <p style={{ color: '#888', fontSize: 12, marginBottom: 10 }}>每行一个邮箱前缀，格式：<code>前缀</code> 或 <code>前缀,密码</code>；上方服务器/域名会应用到本次所有行。</p>
          <form onSubmit={handleBatchCreate}>
            <div className="form-group">
              <label>邮箱列表</label>
              <textarea value={batchText} onChange={e => setBatchText(e.target.value)} rows={8} placeholder="airline-cz-001&#10;airline-cz-002&#10;airline-cz-003,mypassword123" />
            </div>
            <button className="btn btn-primary" type="submit" disabled={creating || !batchText.trim()}>
              {creating && createTab === 'batch' && <span className="spinner" />} 批量创建
            </button>
          </form>
        </div>

        {batchResult && (
          <div className="section" style={{ gridColumn: '1 / -1' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12, marginBottom: 12 }}>
              <h3>创建结果 — 成功 {batchResult.success || 0}，失败 {batchResult.failed || 0}</h3>
              <button className="btn btn-outline btn-sm" type="button" onClick={downloadCredentialCsv}>下载账密 CSV</button>
            </div>
            {(batchResult.results || []).map((r, i) => (
              <div key={i} style={{ padding: '10px 0', borderBottom: '1px solid #eee', fontSize: 13 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <strong>{r.email_address || r.prefix}</strong>
                  {r.password && <code style={{ fontSize: 12 }}>{r.password}</code>}
                  {r.password && <button className="btn btn-sm btn-outline" style={{ padding: '0 6px', height: 22 }} onClick={() => copyText(`${r.email_address},${r.password}`)}>复制账密</button>}
                  <span className={`tag ${STATUS_TAG[r.status] || ''}`}>{r.status}</span>
                  {r.domain && <span style={{ color: '#666' }}>域名：{r.domain}</span>}
                  {r.server_id ? <span style={{ color: '#666' }}>服务器：#{r.server_id}</span> : null}
                </div>
                {r.error && <div style={{ color: '#a82424', fontSize: 12, marginTop: 4 }}>{r.error}</div>}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Integrated mailbox modal */}
      {integratedModal && (
        <div className="modal-overlay" onClick={() => setIntegratedModal(null)}>
          <div className="modal" style={{ width: 460 }} onClick={e => e.stopPropagation()}>
            <h3>{integratedModal.mode === 'add' ? '新增集成邮箱' : `编辑集成邮箱 #${integratedModal.data.id}`}</h3>
            <form onSubmit={async (e) => {
              e.preventDefault()
              setIntegratedSaving(true)
              try {
                if (integratedModal.mode === 'add') await integratedMailboxAPI.create(integratedModal.data)
                else await integratedMailboxAPI.update(integratedModal.data.id, integratedModal.data)
                setIntegratedModal(null)
                setToast({ type: 'success', message: '✅ 保存成功' })
                loadIntegrated()
              } catch (err) { setToast({ type: 'error', message: '❌ ' + err.message }) }
              finally { setIntegratedSaving(false) }
            }}>
              <div className="form-group">
                <label>邮箱地址 *</label>
                <input value={integratedModal.data.email_address} onChange={e => setIntegratedModal({ ...integratedModal, data: { ...integratedModal.data, email_address: e.target.value } })} placeholder="union@asadad.bond" required />
              </div>
              <div className="form-group">
                <label>备注</label>
                <input value={integratedModal.data.display_name || ''} onChange={e => setIntegratedModal({ ...integratedModal, data: { ...integratedModal.data, display_name: e.target.value } })} placeholder="例如: 主汇总 / 备用" />
              </div>
              <div className="modal-footer">
                <button className="btn btn-outline" type="button" onClick={() => setIntegratedModal(null)}>取消</button>
                <button className="btn btn-success" type="submit" disabled={integratedSaving}>{integratedSaving && <span className="spinner" />} 保存</button>
              </div>
            </form>
          </div>
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
