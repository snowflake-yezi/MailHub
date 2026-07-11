import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import {
  ArchiveRestore,
  CheckCircle2,
  Copy,
  Download,
  Inbox,
  KeyRound,
  MailPlus,
  Plus,
  RefreshCw,
  RotateCcw,
  Send,
  Trash2,
  Upload,
  X,
} from 'lucide-react'
import { mailboxAPI, serverAPI, domainAPI, integratedMailboxAPI } from '../api'

const STATUS_META = {
  active: { label: '正常', className: 'tag-success' },
  synced: { label: '已同步', className: 'tag-success' },
  pending: { label: '等待中', className: 'tag-warning' },
  deleting: { label: '删除中', className: 'tag-warning' },
  disabled: { label: '停用', className: 'tag-danger' },
  recycled: { label: '回收站', className: 'tag-warning' },
  sync_failed: { label: '同步失败', className: 'tag-danger' },
  soft_deleted: { label: '可恢复', className: 'tag-warning' },
  purged: { label: '已清理', className: 'tag-info' },
  ok: { label: '正常', className: 'tag-success' },
  fail: { label: '失败', className: 'tag-danger' },
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

function StatusTag({ status }) {
  const meta = STATUS_META[status] || { label: status || '-', className: 'tag-info' }
  return <span className={`tag ${meta.className}`}>{meta.label}</span>
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

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function csvEscape(value) {
  const text = value === undefined || value === null ? '' : String(value)
  if (/[",\n\r]/.test(text)) return `"${text.replace(/"/g, '""')}"`
  return text
}

function downloadCsv(filename, rows) {
  const content = rows.map(row => row.map(csvEscape).join(',')).join('\n')
  const blob = new Blob(['\ufeff' + content], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

function parseMailboxView(value) {
  if (value === 'trash' || value === 'integrated' || value === 'create') return value
  return 'normal'
}

export default function MailboxesPage() {
  const location = useLocation()
  const initialParams = new URLSearchParams(location.search)
  const initialView = parseMailboxView(initialParams.get('view'))
  const [view, setView] = useState(initialView)
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [servers, setServers] = useState([])
  const [domains, setDomains] = useState([])

  const [search, setSearch] = useState(initialParams.get('search') || '')
  const [domainId, setDomainId] = useState(initialParams.get('domain_id') || '')
  const [serverId, setServerId] = useState(initialParams.get('server_id') || '')
  const [statusFilter, setStatusFilter] = useState(initialParams.get('status') || '')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)

  const [createPrefix, setCreatePrefix] = useState('')
  const [createPassword, setCreatePassword] = useState('')
  const [createServerId, setCreateServerId] = useState('0')
  const [createDomainId, setCreateDomainId] = useState('0')
  const [batchText, setBatchText] = useState('')
  const [createTab, setCreateTab] = useState('single')
  const [creating, setCreating] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [batchResult, setBatchResult] = useState(null)
  const csvInputRef = useRef(null)

  const [pwdModal, setPwdModal] = useState(null)
  const [pwdSaving, setPwdSaving] = useState(false)

  const [integrated, setIntegrated] = useState([])
  const [integratedModal, setIntegratedModal] = useState(null)
  const [integratedSaving, setIntegratedSaving] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const params = { page, size, search, domain_id: domainId, server_id: serverId }
      if (view === 'trash') params.view = 'trash'
      else if (statusFilter) params.status = statusFilter

      const data = await mailboxAPI.list(params)
      setItems(Array.isArray(data?.items) ? data.items : Array.isArray(data) ? data : [])
      setTotal(data?.total_count ?? data?.total ?? 0)
    } catch (e) {
      setToast({ type: 'error', message: '加载失败: ' + e.message })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [view, search, domainId, serverId, statusFilter, page, size])

  useEffect(() => {
    const params = new URLSearchParams(location.search)
    const nextView = parseMailboxView(params.get('view'))
    setView(nextView)
    setSearch(params.get('search') || '')
    setDomainId(params.get('domain_id') || '')
    setServerId(params.get('server_id') || '')
    setStatusFilter(params.get('status') || '')
    setPage(1)
  }, [location.search])

  useEffect(() => {
    if (view === 'normal' || view === 'trash') load()
  }, [load, view])

  useEffect(() => {
    serverAPI.list().then(d => setServers(Array.isArray(d) ? d : [])).catch(() => {})
    domainAPI.list().then(d => setDomains(Array.isArray(d) ? d : [])).catch(() => {})
  }, [])

  const loadIntegrated = useCallback(() => {
    integratedMailboxAPI.list()
      .then(d => setIntegrated(Array.isArray(d) ? d : []))
      .catch(e => setToast({ type: 'error', message: '加载集成邮箱失败: ' + e.message }))
  }, [])

  useEffect(() => {
    if (view === 'integrated') loadIntegrated()
  }, [view, loadIntegrated])

  const switchView = (nextView) => {
    setView(nextView)
    setPage(1)
    if (nextView === 'integrated' && view === 'integrated') loadIntegrated()
    if (nextView === 'create') {
      window.setTimeout(() => {
        document.getElementById('mailbox-create-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }, 0)
    }
  }

  const summary = useMemo(() => ({
    total,
    domains: domainId ? 1 : domains.length,
    servers: serverId ? 1 : servers.length,
    integrated: integrated.length,
  }), [total, domainId, domains.length, serverId, servers.length, integrated.length])

  const serverDomains = (server) => server?.domains || server?.Domains || []
  const selectedCreateServer = servers.find(s => s.id === parseInt(createServerId, 10))
  const selectedCreateDomain = domains.find(d => d.id === parseInt(createDomainId, 10))
  const domainIsServedByServer = (server, domainValue) => {
    if (!server || !domainValue || domainValue === '0') return true
    return serverDomains(server).some(d => d.id === parseInt(domainValue, 10))
  }
  const createServerOptions = createDomainId !== '0'
    ? servers.filter(s => domainIsServedByServer(s, createDomainId))
    : servers
  const createDomainOptions = createServerId !== '0' && selectedCreateServer
    ? serverDomains(selectedCreateServer)
    : domains

  const handleSearch = (e) => {
    e.preventDefault()
    setPage(1)
    load(true)
  }

  const handleCreateServerChange = (value) => {
    setCreateServerId(value)
    const server = servers.find(s => s.id === parseInt(value, 10))
    if (value !== '0' && createDomainId !== '0' && !domainIsServedByServer(server, createDomainId)) {
      setCreateDomainId('0')
      setToast({ type: 'error', message: '已清空域名筛选：该服务器未绑定当前域名' })
    }
  }

  const handleCreateDomainChange = (value) => {
    setCreateDomainId(value)
    if (value !== '0' && createServerId !== '0' && !domainIsServedByServer(selectedCreateServer, value)) {
      setCreateServerId('0')
      setToast({ type: 'error', message: '已清空服务器筛选：当前域名未绑定该服务器' })
    }
  }

  const buildCreateItem = (prefix, password = '') => ({
    prefix: prefix.trim(),
    password: password.trim(),
    server_id: parseInt(createServerId, 10) || 0,
    domain_id: parseInt(createDomainId, 10) || 0,
  })

  const submitCreateItems = async (createItems) => {
    const validItems = createItems.filter(item => item.prefix)
    if (validItems.length === 0) return
    setCreating(true)
    setBatchResult(null)
    try {
      const result = await mailboxAPI.batchCreate(validItems)
      setBatchResult(result)
      setToast({ type: 'success', message: `创建完成：成功 ${result.success || 0}，失败 ${result.failed || 0}` })
      load(true)
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

  const handleCsvUpload = async (event) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    if (!/\.(csv|txt)$/i.test(file.name)) {
      setToast({ type: 'error', message: '仅支持 CSV 或 TXT 文件' })
      return
    }
    setCreateTab('upload')
    setUploading(true)
    setBatchResult(null)
    try {
      const result = await mailboxAPI.upload(
        file,
        parseInt(createServerId, 10) || 0,
        parseInt(createDomainId, 10) || 0,
      )
      setBatchResult(result)
      setToast({ type: 'success', message: `文件导入完成：成功 ${result.success || 0}，失败 ${result.failed || 0}` })
      load(true)
    } catch (e) {
      setToast({ type: 'error', message: '文件导入失败: ' + e.message })
    } finally {
      setUploading(false)
    }
  }

  const downloadCredentialCsv = () => {
    const rows = [
      ['email_address', 'password', 'prefix', 'domain', 'server_id', 'status', 'error'],
      ...(batchResult?.results || []).map(r => [r.email_address || '', r.password || '', r.prefix || '', r.domain || '', r.server_id || '', r.status || '', r.error || '']),
    ]
    const ts = new Date().toISOString().replace(/[-:T]/g, '').slice(0, 12)
    downloadCsv(`mailbox-credentials-${ts}.csv`, rows)
  }

  const copyText = (text) => {
    navigator.clipboard.writeText(text).then(() => {
      setToast({ type: 'success', message: '已复制' })
    })
  }

  const askDelete = (item) => {
    setConfirm({
      title: '删除邮箱',
      message: `确定删除 ${item.email_address} 吗？系统会摘除收信并把 Maildir 移入回收站。`,
      confirmLabel: '删除',
      onConfirm: async () => {
        try {
          await mailboxAPI.remove(item.id)
          setToast({ type: 'success', message: '已提交删除: ' + item.email_address })
          load(true)
        } catch (e) {
          setToast({ type: 'error', message: '删除失败: ' + e.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const askRestore = (item) => {
    setConfirm({
      title: '恢复邮箱',
      message: `确定恢复 ${item.email_address} 吗？仅删除后 24 小时内可从远端回收站恢复。`,
      confirmLabel: '恢复',
      danger: false,
      onConfirm: async () => {
        try {
          const data = await mailboxAPI.restore(item.id)
          setToast({ type: 'success', message: data?.password ? `已恢复，临时密码：${data.password}` : '已恢复: ' + item.email_address })
          load(true)
        } catch (e) {
          setToast({ type: 'error', message: '恢复失败: ' + e.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const askPurge = (item) => {
    setConfirm({
      title: '彻底清理邮箱',
      message: `确定永久清理 ${item.email_address} 吗？此操作不可恢复。`,
      confirmLabel: '彻底清理',
      onConfirm: async () => {
        try {
          await mailboxAPI.purge(item.id)
          setToast({ type: 'success', message: '已彻底清理: ' + item.email_address })
          load(true)
        } catch (e) {
          setToast({ type: 'error', message: '清理失败: ' + e.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const handlePwdSave = async () => {
    if (!pwdModal.password || pwdModal.password.length < 6) {
      setToast({ type: 'error', message: '密码至少 6 个字符' })
      return
    }
    setPwdSaving(true)
    try {
      await mailboxAPI.updatePassword(pwdModal.id, pwdModal.password)
      setPwdModal(null)
      setToast({ type: 'success', message: '密码修改成功' })
      load(true)
    } catch (e) {
      setToast({ type: 'error', message: '修改失败: ' + e.message })
    } finally {
      setPwdSaving(false)
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / size))
  const safePage = Math.min(page, totalPages)

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>邮箱账户</h1>
          <p className="page-subtitle">按服务器、域名和生命周期管理邮箱，兼顾批量创建、回收站恢复与转发目标池。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => load(true)} disabled={refreshing}>
            {refreshing ? <span className="spinner" /> : <RefreshCw size={16} />}
            刷新
          </button>
          <button className="btn btn-primary" type="button" onClick={() => switchView('create')}>
            <MailPlus size={16} /> 创建邮箱
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <SummaryTile icon={Inbox} label="筛选结果" value={summary.total} tone="brand" />
        <SummaryTile icon={CheckCircle2} label="可用域名" value={summary.domains} tone="success" />
        <SummaryTile icon={Send} label="可用节点" value={summary.servers} tone="info" />
        <SummaryTile icon={ArchiveRestore} label="转发目标" value={summary.integrated} tone="warning" />
      </div>

      <div className="phase-tabs">
        {[
          ['normal', '账户集合'],
          ['trash', '回收站'],
          ['integrated', '集成邮箱'],
          ['create', '创建邮箱'],
        ].map(([value, label]) => (
          <button
            key={value}
            className={view === value ? 'active' : ''}
            type="button"
            onClick={() => switchView(value)}
          >
            {label}
          </button>
        ))}
      </div>

      {(view === 'normal' || view === 'trash') && (
        <section className="section data-section">
          <div className="panel-header mailbox-toolbar-header">
            <div>
              <h3>{view === 'trash' ? '回收站账户' : '账户列表'}</h3>
              <div className="panel-caption">
                {view === 'trash' ? '远端可恢复窗口为删除后 24 小时，彻底清理不可撤销。' : '筛选条件会同步作用于分页查询。'}
              </div>
            </div>
          </div>

          <form className="mailbox-toolbar" onSubmit={handleSearch}>
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
                <option value="active">正常</option>
                <option value="deleting">删除中</option>
              </select>
            )}
            <button className="btn btn-primary" type="submit">筛选</button>
            <button className="btn btn-outline" type="button" onClick={() => { setSearch(''); setDomainId(''); setServerId(''); setStatusFilter(''); setPage(1) }}>
              <RotateCcw size={15} /> 重置
            </button>
          </form>

          <div className="table-wrap">
            <table className="data-table mailbox-table">
              <thead>
                <tr>
                  <th>邮箱</th>
                  {view === 'normal' && <th>密码</th>}
                  <th>域名</th>
                  <th>服务器</th>
                  <th>状态</th>
                  {view === 'normal' && <th>同步</th>}
                  <th>{view === 'trash' ? '删除时间' : '创建时间'}</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map(item => (
                  <tr key={item.id}>
                    <td>
                      <div className="entity-cell">
                        <span className="entity-icon"><Inbox size={16} /></span>
                        <div>
                          <strong>{item.email_address}</strong>
                          <span>#{item.id}</span>
                        </div>
                        <button className="icon-button compact" type="button" title="复制邮箱" onClick={() => copyText(item.email_address)}>
                          <Copy size={14} />
                        </button>
                      </div>
                    </td>
                    {view === 'normal' && (
                      <td>
                        {item.password ? (
                          <div className="secret-cell">
                            <code>{item.password}</code>
                            <button className="icon-button compact" type="button" title="复制密码" onClick={() => copyText(item.password)}>
                              <Copy size={14} />
                            </button>
                          </div>
                        ) : <span className="muted-text">未记录</span>}
                      </td>
                    )}
                    <td>{item.domain?.name || `#${item.domain_id}`}</td>
                    <td>{item.server?.name || `#${item.server_id}`}</td>
                    <td><StatusTag status={item.status} /></td>
                    {view === 'normal' && <td>{item.sync_status ? <StatusTag status={item.sync_status} /> : <span className="muted-text">-</span>}</td>}
                    <td>{formatDate(view === 'trash' ? item.recycled_at : item.created_at)}</td>
                    <td>
                      <div className="row-actions">
                        {view === 'normal' && (
                          <>
                            <button className="icon-button compact" type="button" title="修改密码" onClick={() => setPwdModal({ id: item.id, email: item.email_address, password: '' })}>
                              <KeyRound size={15} />
                            </button>
                            {(item.status === 'active' || item.status === 'deleting') && (
                              <button className="icon-button compact danger" type="button" title="删除" onClick={() => askDelete(item)}>
                                <Trash2 size={15} />
                              </button>
                            )}
                            {item.status === 'soft_deleted' && (
                              <button className="icon-button compact" type="button" title="恢复" onClick={() => askRestore(item)}>
                                <ArchiveRestore size={15} />
                              </button>
                            )}
                          </>
                        )}
                        {view === 'trash' && item.status === 'soft_deleted' && (
                          <>
                            <button className="icon-button compact" type="button" title="恢复" onClick={() => askRestore(item)}>
                              <ArchiveRestore size={15} />
                            </button>
                            <button className="icon-button compact danger" type="button" title="彻底清理" onClick={() => askPurge(item)}>
                              <Trash2 size={15} />
                            </button>
                          </>
                        )}
                        {view === 'trash' && item.status === 'purged' && <span className="muted-text">已清理</span>}
                      </div>
                    </td>
                  </tr>
                ))}
                {items.length === 0 && (
                  <tr>
                    <td colSpan={view === 'normal' ? 8 : 6}>
                      <div className="empty-state">
                        {loading ? <span className="spinner" /> : <Inbox size={28} />}
                        <strong>{loading ? '加载中...' : view === 'trash' ? '回收站为空' : '暂无邮箱账户'}</strong>
                        {!loading && <span>{view === 'trash' ? '删除后的邮箱会在这里显示，可在恢复窗口内处理。' : '创建第一个邮箱后即可开始收信和查询。'}</span>}
                      </div>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {total > 0 && (
            <div className="pagination-bar">
              <button className="btn btn-sm btn-outline" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>上一页</button>
              <span>第 <strong>{safePage}</strong> / {totalPages} 页，共 {total} 条</span>
              <button className="btn btn-sm btn-outline" disabled={safePage >= totalPages} onClick={() => setPage(safePage + 1)}>下一页</button>
              <span className="pagination-spacer" />
              <label>
                每页
                <select value={size} onChange={e => { setSize(parseInt(e.target.value, 10)); setPage(1) }}>
                  <option value={20}>20</option>
                  <option value={50}>50</option>
                  <option value={100}>100</option>
                </select>
              </label>
            </div>
          )}
        </section>
      )}

      {view === 'integrated' && (
        <section className="section data-section">
          <div className="panel-header">
            <div>
              <h3>集成邮箱</h3>
              <div className="panel-caption">所有非垃圾邮件会汇总转发到当前生效目标，切换后 mail-node 自动拉取生效。</div>
            </div>
            <button className="btn btn-primary" type="button" onClick={() => setIntegratedModal({ mode: 'add', data: { email_address: '', display_name: '' } })}>
              <Plus size={16} /> 新增目标
            </button>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>邮箱地址</th><th>备注</th><th>状态</th><th>操作</th></tr>
              </thead>
              <tbody>
                {integrated.map(m => (
                  <tr key={m.id}>
                    <td><code>{m.email_address}</code></td>
                    <td>{m.display_name || '-'}</td>
                    <td>{m.is_active ? <span className="tag tag-success">当前生效</span> : <span className="muted-text">备用</span>}</td>
                    <td>
                      <div className="row-actions">
                        {!m.is_active && (
                          <button className="btn btn-sm btn-success" type="button" onClick={async () => {
                            try {
                              await integratedMailboxAPI.activate(m.id)
                              setToast({ type: 'success', message: '已设为当前转发目标' })
                              loadIntegrated()
                            } catch (e) {
                              setToast({ type: 'error', message: e.message })
                            }
                          }}>设为当前</button>
                        )}
                        <button className="btn btn-sm btn-outline" type="button" onClick={() => setIntegratedModal({ mode: 'edit', data: { ...m } })}>编辑</button>
                        <button className="btn btn-sm btn-danger" type="button" disabled={m.is_active} onClick={async () => {
                          setConfirm({
                            title: '删除集成邮箱',
                            message: `确定删除 ${m.email_address} 吗？当前生效目标不能删除。`,
                            confirmLabel: '删除',
                            onConfirm: async () => {
                              try {
                                await integratedMailboxAPI.remove(m.id)
                                setToast({ type: 'success', message: '已删除' })
                                loadIntegrated()
                              } catch (e) {
                                setToast({ type: 'error', message: e.message })
                              }
                              setConfirm(null)
                            },
                            onCancel: () => setConfirm(null),
                          })
                        }}>删除</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {integrated.length === 0 && (
                  <tr><td colSpan={4}><div className="empty-state"><Send size={28} /><strong>暂无集成邮箱</strong><span>添加一个目标后即可接收转发汇总。</span></div></td></tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {view === 'create' && (
        <section id="mailbox-create-panel" className="section mailbox-create">
          <div className="panel-header">
            <div>
              <h3>创建邮箱</h3>
              <div className="panel-caption">服务器和域名均可指定，不指定时由系统按健康节点和域名池自动分配。</div>
            </div>
            {createServerId === '0' && createDomainId === '0' && <span className="tag tag-info">自动分配</span>}
          </div>

          <div className="field-grid">
            <div className="form-group">
              <label>邮箱服务器</label>
              <select value={createServerId} onChange={e => handleCreateServerChange(e.target.value)}>
                <option value="0">自动选择服务器</option>
                {createServerOptions.map(s => <option key={s.id} value={s.id}>{s.name} ({s.status}, {s.current_load}/{s.capacity})</option>)}
              </select>
              {createDomainId !== '0' && <div className="form-hint">已按域名 {selectedCreateDomain?.name || `#${createDomainId}`} 限制服务器范围。</div>}
            </div>
            <div className="form-group">
              <label>域名</label>
              <select value={createDomainId} onChange={e => handleCreateDomainChange(e.target.value)}>
                <option value="0">自动选择域名</option>
                {createDomainOptions.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
              {createServerId !== '0' && <div className="form-hint">已按服务器 {selectedCreateServer?.name || `#${createServerId}`} 限制域名范围。</div>}
            </div>
          </div>

          <div className="create-grid">
            <form className="create-card" onSubmit={handleSingleCreate}>
              <h4>单个创建</h4>
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

            <form className="create-card" onSubmit={handleBatchCreate}>
              <h4>批量创建</h4>
              <div className="form-group">
                <label>邮箱列表</label>
                <textarea value={batchText} onChange={e => setBatchText(e.target.value)} rows={7} placeholder={'airline-cz-001\nairline-cz-002\nairline-cz-003,mypassword123'} />
                <div className="form-hint">每行一个前缀；文件格式为 prefix,password,domain_id,server_id，后 3 列可省略。</div>
              </div>
              <div className="create-actions">
                <button className="btn btn-primary" type="submit" disabled={creating || uploading || !batchText.trim()}>
                  {creating && createTab === 'batch' && <span className="spinner" />} 批量创建
                </button>
                <input ref={csvInputRef} className="visually-hidden" type="file" accept=".csv,.txt,text/csv,text/plain" onChange={handleCsvUpload} />
                <button className="btn btn-outline" type="button" title="支持 CSV 或 TXT 文件" disabled={creating || uploading} onClick={() => csvInputRef.current?.click()}>
                  {uploading ? <span className="spinner" /> : <Upload size={16} />} 上传文件
                </button>
                <span className="upload-format-hint">支持 .csv / .txt</span>
              </div>
            </form>
          </div>

          {batchResult && (
            <div className="batch-result">
              <div className="panel-header">
                <div>
                  <h3>创建结果</h3>
                  <div className="panel-caption">成功 {batchResult.success || 0}，失败 {batchResult.failed || 0}</div>
                </div>
                <div className="row-actions">
                  <button className="btn btn-outline" type="button" onClick={() => switchView('normal')}>
                    查看账号集合
                  </button>
                  <button className="btn btn-outline" type="button" onClick={downloadCredentialCsv}>
                    <Download size={16} /> 下载账密 CSV
                  </button>
                </div>
              </div>
              <div className="result-list">
                {(batchResult.results || []).map((r, i) => (
                  <div className="result-item" key={`${r.email_address || r.prefix}-${i}`}>
                    <div>
                      <strong>{r.email_address || r.prefix}</strong>
                      <span>{r.domain || '-'} {r.server_id ? `#${r.server_id}` : ''}</span>
                    </div>
                    {r.password && <code>{r.password}</code>}
                    <StatusTag status={r.status} />
                    {r.password && <button className="icon-button compact" type="button" title="复制账密" onClick={() => copyText(`${r.email_address},${r.password}`)}><Copy size={14} /></button>}
                    {r.error && <span className="result-error">{r.error}</span>}
                  </div>
                ))}
              </div>
            </div>
          )}
        </section>
      )}

      {integratedModal && (
        <div className="drawer-overlay" onClick={() => setIntegratedModal(null)}>
          <aside className="drawer" onClick={e => e.stopPropagation()} aria-label="集成邮箱编辑">
            <div className="drawer-header">
              <div>
                <div className="drawer-kicker">Forward target</div>
                <h2>{integratedModal.mode === 'add' ? '新增集成邮箱' : `编辑 #${integratedModal.data.id}`}</h2>
              </div>
              <button className="icon-button" type="button" title="关闭" onClick={() => setIntegratedModal(null)}><X size={18} /></button>
            </div>
            <form className="drawer-body" onSubmit={async (e) => {
              e.preventDefault()
              setIntegratedSaving(true)
              try {
                if (integratedModal.mode === 'add') await integratedMailboxAPI.create(integratedModal.data)
                else await integratedMailboxAPI.update(integratedModal.data.id, integratedModal.data)
                setIntegratedModal(null)
                setToast({ type: 'success', message: '保存成功' })
                loadIntegrated()
              } catch (err) {
                setToast({ type: 'error', message: err.message })
              } finally {
                setIntegratedSaving(false)
              }
            }}>
              <div className="form-group">
                <label>邮箱地址</label>
                <input value={integratedModal.data.email_address} onChange={e => setIntegratedModal({ ...integratedModal, data: { ...integratedModal.data, email_address: e.target.value } })} placeholder="union@example.com" required />
              </div>
              <div className="form-group">
                <label>备注</label>
                <input value={integratedModal.data.display_name || ''} onChange={e => setIntegratedModal({ ...integratedModal, data: { ...integratedModal.data, display_name: e.target.value } })} placeholder="例如：主汇总 / 备用" />
              </div>
              <div className="drawer-footer">
                <button className="btn btn-outline" type="button" onClick={() => setIntegratedModal(null)}>取消</button>
                <button className="btn btn-primary" type="submit" disabled={integratedSaving}>{integratedSaving && <span className="spinner" />} 保存</button>
              </div>
            </form>
          </aside>
        </div>
      )}

      {pwdModal && (
        <div className="drawer-overlay" onClick={() => setPwdModal(null)}>
          <aside className="drawer" onClick={e => e.stopPropagation()} aria-label="修改邮箱密码">
            <div className="drawer-header">
              <div>
                <div className="drawer-kicker">Credential</div>
                <h2>修改邮箱密码</h2>
              </div>
              <button className="icon-button" type="button" title="关闭" onClick={() => setPwdModal(null)}><X size={18} /></button>
            </div>
            <div className="drawer-body">
              <div className="form-group">
                <label>邮箱地址</label>
                <input value={pwdModal.email} readOnly />
              </div>
              <div className="form-group">
                <label>新密码</label>
                <input value={pwdModal.password} onChange={e => setPwdModal({ ...pwdModal, password: e.target.value })} placeholder="至少 6 个字符" autoFocus />
              </div>
              <div className="drawer-footer">
                <button className="btn btn-outline" type="button" onClick={() => setPwdModal(null)}>取消</button>
                <button className="btn btn-primary" type="button" onClick={handlePwdSave} disabled={pwdSaving}>
                  {pwdSaving && <span className="spinner" />} 保存
                </button>
              </div>
            </div>
          </aside>
        </div>
      )}

      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
