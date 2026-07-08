import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import {
  ArrowUpRight,
  Inbox,
  Mail,
  Search,
  Server,
} from 'lucide-react'
import { emailAPI, mailboxAPI, serverAPI } from '../api'

function isEmail(value) {
  return /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(String(value || '').trim())
}

function isMessageID(value) {
  const text = String(value || '').trim()
  return text.startsWith('<') || /^message-id\s*[:：]/i.test(text)
}

function normalizeMessageID(value) {
  return String(value || '').trim().replace(/^message-id\s*[:：]\s*/i, '').trim()
}

function matchesServer(server, query) {
  const needle = query.toLowerCase()
  const domainText = (server.domains || []).map(domain => domain.name).join(' ')
  return [
    server.id,
    server.name,
    server.api_host,
    server.status,
    domainText,
  ].some(value => String(value || '').toLowerCase().includes(needle))
}

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function SearchSection({ icon: Icon, title, caption, children, action }) {
  return (
    <section className="section data-section search-result-section">
      <div className="panel-header">
        <div className="drawer-title-with-icon">
          <span className="module-icon"><Icon size={20} /></span>
          <div>
            <h3>{title}</h3>
            <div className="panel-caption">{caption}</div>
          </div>
        </div>
        {action}
      </div>
      {children}
    </section>
  )
}

export default function SearchPage() {
  const [params] = useSearchParams()
  const query = (params.get('q') || '').trim()
  const [mailboxes, setMailboxes] = useState([])
  const [mailboxTotal, setMailboxTotal] = useState(0)
  const [servers, setServers] = useState([])
  const [messages, setMessages] = useState([])
  const [loading, setLoading] = useState(false)
  const [errors, setErrors] = useState({})

  const canSearchEmailMessages = isEmail(query)
  const messageID = isMessageID(query) ? normalizeMessageID(query) : ''

  const load = useCallback(async () => {
    if (!query) return
    setLoading(true)
    setErrors({})
    setMessages([])
    try {
      const [mailboxResult, serverResult] = await Promise.allSettled([
        mailboxAPI.list({ search: query, page: 1, size: 8 }),
        serverAPI.list(),
      ])

      if (mailboxResult.status === 'fulfilled') {
        const data = mailboxResult.value
        setMailboxes(Array.isArray(data?.items) ? data.items : Array.isArray(data) ? data : [])
        setMailboxTotal(data?.total_count ?? data?.total ?? 0)
      } else {
        setMailboxes([])
        setMailboxTotal(0)
        setErrors(prev => ({ ...prev, mailboxes: mailboxResult.reason?.message || '邮箱搜索失败' }))
      }

      if (serverResult.status === 'fulfilled') {
        const list = Array.isArray(serverResult.value) ? serverResult.value : []
        setServers(list.filter(server => matchesServer(server, query)))
      } else {
        setServers([])
        setErrors(prev => ({ ...prev, servers: serverResult.reason?.message || '服务器搜索失败' }))
      }

      if (canSearchEmailMessages) {
        try {
          const data = await emailAPI.list(query, 1, 8)
          setMessages(Array.isArray(data?.messages) ? data.messages : Array.isArray(data) ? data : [])
        } catch (err) {
          setErrors(prev => ({ ...prev, emails: err.message }))
        }
      }
    } finally {
      setLoading(false)
    }
  }, [canSearchEmailMessages, query])

  useEffect(() => {
    load()
  }, [load])

  const summary = useMemo(() => ({
    mailboxes: mailboxTotal || mailboxes.length,
    servers: servers.length,
    messages: messages.length,
  }), [mailboxTotal, mailboxes.length, messages.length, servers.length])

  if (!query) {
    return (
      <section className="section empty-state">
        <Search size={30} />
        <strong>输入关键词开始搜索</strong>
        <span>可以搜索邮箱地址、邮箱前缀、服务器名称、API 地址或绑定域名。</span>
      </section>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>搜索结果</h1>
          <p className="page-subtitle">关键词「{query}」会同时匹配邮箱账户、服务器节点；邮箱地址还会拉取邮件列表。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={load} disabled={loading}>
            {loading ? <span className="spinner" /> : <Search size={16} />}
            重新搜索
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <div className="summary-tile" data-tone="brand">
          <span className="summary-icon"><Inbox size={18} /></span>
          <div>
            <div className="summary-value">{summary.mailboxes}</div>
            <div className="summary-label">邮箱匹配</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="info">
          <span className="summary-icon"><Server size={18} /></span>
          <div>
            <div className="summary-value">{summary.servers}</div>
            <div className="summary-label">节点匹配</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="success">
          <span className="summary-icon"><Mail size={18} /></span>
          <div>
            <div className="summary-value">{canSearchEmailMessages ? summary.messages : '-'}</div>
            <div className="summary-label">邮件匹配</div>
          </div>
        </div>
      </div>

      {messageID && (
        <SearchSection
          icon={Mail}
          title="Message-ID"
          caption="Message-ID 需要邮箱上下文才能读取正文。"
          action={<Link className="btn btn-sm btn-outline" to={`/emails?message_id=${encodeURIComponent(messageID)}`}>去邮件页 <ArrowUpRight size={14} /></Link>}
        >
          <div className="empty-state">
            <Mail size={28} />
            <strong>需要先指定邮箱地址</strong>
            <span>请在邮件查询页输入邮箱地址，或使用带 mailbox 参数的链接打开具体 Message-ID。</span>
          </div>
        </SearchSection>
      )}

      <SearchSection
        icon={Inbox}
        title="邮箱账户"
        caption={errors.mailboxes || `按邮箱地址和前缀匹配，展示前 ${mailboxes.length} 条。`}
        action={<Link className="btn btn-sm btn-outline" to={`/mailboxes?search=${encodeURIComponent(query)}`}>查看邮箱 <ArrowUpRight size={14} /></Link>}
      >
        {mailboxes.length > 0 ? (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>邮箱</th><th>域名</th><th>服务器</th><th>状态</th><th>创建时间</th></tr>
              </thead>
              <tbody>
                {mailboxes.map(item => (
                  <tr key={item.id}>
                    <td><code>{item.email_address}</code></td>
                    <td>{item.domain?.name || `#${item.domain_id}`}</td>
                    <td>{item.server?.name || `#${item.server_id}`}</td>
                    <td><span className="tag tag-info">{item.status || '-'}</span></td>
                    <td>{formatDate(item.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="empty-state">
            <Inbox size={28} />
            <strong>{errors.mailboxes ? '邮箱搜索失败' : '没有匹配邮箱'}</strong>
            <span>{errors.mailboxes || '换个邮箱地址或前缀试试。'}</span>
          </div>
        )}
      </SearchSection>

      <SearchSection
        icon={Server}
        title="服务器节点"
        caption={errors.servers || '匹配节点名称、API 地址、状态和绑定域名。'}
        action={<Link className="btn btn-sm btn-outline" to={`/servers?search=${encodeURIComponent(query)}`}>查看节点 <ArrowUpRight size={14} /></Link>}
      >
        {servers.length > 0 ? (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>节点</th><th>API</th><th>状态</th><th>负载</th><th>绑定域名</th></tr>
              </thead>
              <tbody>
                {servers.map(server => (
                  <tr key={server.id}>
                    <td><strong>{server.name || `server-${server.id}`}</strong></td>
                    <td><code>{server.api_host || '-'}</code></td>
                    <td><span className="tag tag-info">{server.status || '-'}</span></td>
                    <td>{server.current_load || 0} / {server.capacity || 0}</td>
                    <td>{(server.domains || []).map(domain => domain.name).join(', ') || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="empty-state">
            <Server size={28} />
            <strong>{errors.servers ? '节点搜索失败' : '没有匹配节点'}</strong>
            <span>{errors.servers || '可以搜索节点名、API 地址或绑定域名。'}</span>
          </div>
        )}
      </SearchSection>

      {canSearchEmailMessages && (
        <SearchSection
          icon={Mail}
          title="邮件列表"
          caption={errors.emails || `邮箱 ${query} 的最近邮件。`}
          action={<Link className="btn btn-sm btn-outline" to={`/emails?mailbox=${encodeURIComponent(query)}`}>打开邮件查询 <ArrowUpRight size={14} /></Link>}
        >
          {messages.length > 0 ? (
            <div className="email-list search-email-list">
              {messages.map(msg => (
                <Link
                  className="email-list-item"
                  key={msg.message_id}
                  to={`/emails?mailbox=${encodeURIComponent(query)}&message_id=${encodeURIComponent(msg.message_id)}`}
                >
                  <div className="email-list-top">
                    <strong>{msg.subject || '(无标题)'}</strong>
                    {(msg.attachments_count || 0) > 0 && <span className="tag tag-info">{msg.attachments_count} 附件</span>}
                  </div>
                  <div className="email-list-meta">
                    <span>{msg.from || '-'}</span>
                    <span>{formatDate(msg.date || msg.received_at)}</span>
                  </div>
                  <p>{msg.text_preview || '无正文预览'}</p>
                </Link>
              ))}
            </div>
          ) : (
            <div className="empty-state">
              <Mail size={28} />
              <strong>{errors.emails ? '邮件搜索失败' : '暂无邮件'}</strong>
              <span>{errors.emails || '该邮箱当前没有可展示的结构化邮件。'}</span>
            </div>
          )}
        </SearchSection>
      )}
    </div>
  )
}
