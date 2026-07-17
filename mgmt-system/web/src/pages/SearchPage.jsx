import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  ArrowUpRight,
  Inbox,
  Mail,
  Search,
  Server,
} from 'lucide-react'
import { emailAPI, mailboxAPI, serverAPI } from '../api'
import { formatDateTime } from '../i18n'

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
  const { t } = useTranslation('pages')
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
        setErrors(prev => ({ ...prev, mailboxes: mailboxResult.reason?.message || t('search.mailboxes.failed') }))
      }

      if (serverResult.status === 'fulfilled') {
        const list = Array.isArray(serverResult.value) ? serverResult.value : []
        setServers(list.filter(server => matchesServer(server, query)))
      } else {
        setServers([])
        setErrors(prev => ({ ...prev, servers: serverResult.reason?.message || t('search.servers.failed') }))
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
  }, [canSearchEmailMessages, query, t])

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
        <strong>{t('search.emptyTitle')}</strong>
        <span>{t('search.emptyDesc')}</span>
      </section>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('search.title')}</h1>
          <p className="page-subtitle">{t('search.subtitle', { query })}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={load} disabled={loading}>
            {loading ? <span className="spinner" /> : <Search size={16} />}
            {t('search.retry')}
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <div className="summary-tile" data-tone="brand">
          <span className="summary-icon"><Inbox size={18} /></span>
          <div>
            <div className="summary-value">{summary.mailboxes}</div>
            <div className="summary-label">{t('search.summary.mailboxes')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="info">
          <span className="summary-icon"><Server size={18} /></span>
          <div>
            <div className="summary-value">{summary.servers}</div>
            <div className="summary-label">{t('search.summary.servers')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="success">
          <span className="summary-icon"><Mail size={18} /></span>
          <div>
            <div className="summary-value">{canSearchEmailMessages ? summary.messages : '-'}</div>
            <div className="summary-label">{t('search.summary.emails')}</div>
          </div>
        </div>
      </div>

      {messageID && (
        <SearchSection
          icon={Mail}
          title="Message-ID"
          caption={t('search.messageId.caption')}
          action={<Link className="btn btn-sm btn-outline" to={`/emails?message_id=${encodeURIComponent(messageID)}`}>{t('search.messageId.action')} <ArrowUpRight size={14} /></Link>}
        >
          <div className="empty-state">
            <Mail size={28} />
            <strong>{t('search.messageId.title')}</strong>
            <span>{t('search.messageId.desc')}</span>
          </div>
        </SearchSection>
      )}

      <SearchSection
        icon={Inbox}
        title={t('search.mailboxes.title')}
        caption={errors.mailboxes || t('search.mailboxes.caption', { count: mailboxes.length })}
        action={<Link className="btn btn-sm btn-outline" to={`/mailboxes?search=${encodeURIComponent(query)}`}>{t('search.mailboxes.action')} <ArrowUpRight size={14} /></Link>}
      >
        {mailboxes.length > 0 ? (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>{t('search.columns.mailbox')}</th><th>{t('search.columns.domain')}</th><th>{t('search.columns.server')}</th><th>{t('search.columns.status')}</th><th>{t('search.columns.createdAt')}</th></tr>
              </thead>
              <tbody>
                {mailboxes.map(item => (
                  <tr key={item.id}>
                    <td><code>{item.email_address}</code></td>
                    <td>{item.domain?.name || `#${item.domain_id}`}</td>
                    <td>{item.server?.name || `#${item.server_id}`}</td>
                    <td><span className="tag tag-info">{t(`mailboxes.status.${item.status}`, { defaultValue: item.status || '-' })}</span></td>
                    <td>{formatDateTime(item.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="empty-state">
            <Inbox size={28} />
            <strong>{errors.mailboxes ? t('search.mailboxes.failed') : t('search.mailboxes.empty')}</strong>
            <span>{errors.mailboxes || t('search.mailboxes.emptyDesc')}</span>
          </div>
        )}
      </SearchSection>

      <SearchSection
        icon={Server}
        title={t('search.servers.title')}
        caption={errors.servers || t('search.servers.caption')}
        action={<Link className="btn btn-sm btn-outline" to={`/servers?search=${encodeURIComponent(query)}`}>{t('search.servers.action')} <ArrowUpRight size={14} /></Link>}
      >
        {servers.length > 0 ? (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>{t('search.columns.node')}</th><th>API</th><th>{t('search.columns.status')}</th><th>{t('search.columns.load')}</th><th>{t('search.columns.domains')}</th></tr>
              </thead>
              <tbody>
                {servers.map(server => (
                  <tr key={server.id}>
                    <td><strong>{server.name || `server-${server.id}`}</strong></td>
                    <td><code>{server.api_host || '-'}</code></td>
                    <td><span className="tag tag-info">{t(`servers.status.${server.status}`, { defaultValue: server.status || '-' })}</span></td>
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
            <strong>{errors.servers ? t('search.servers.failed') : t('search.servers.empty')}</strong>
            <span>{errors.servers || t('search.servers.emptyDesc')}</span>
          </div>
        )}
      </SearchSection>

      {canSearchEmailMessages && (
        <SearchSection
          icon={Mail}
          title={t('search.emails.title')}
          caption={errors.emails || t('search.emails.caption', { query })}
          action={<Link className="btn btn-sm btn-outline" to={`/emails?mailbox=${encodeURIComponent(query)}`}>{t('search.emails.action')} <ArrowUpRight size={14} /></Link>}
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
                    <strong>{msg.subject || t('search.emails.noSubject')}</strong>
                    {(msg.attachments_count || 0) > 0 && <span className="tag tag-info">{t('search.emails.attachments', { count: msg.attachments_count })}</span>}
                  </div>
                  <div className="email-list-meta">
                    <span>{msg.from || '-'}</span>
                    <span>{formatDateTime(msg.date || msg.received_at)}</span>
                  </div>
                  <p>{msg.text_preview || t('search.emails.noPreview')}</p>
                </Link>
              ))}
            </div>
          ) : (
            <div className="empty-state">
              <Mail size={28} />
              <strong>{errors.emails ? t('search.emails.failed') : t('search.emails.empty')}</strong>
              <span>{errors.emails || t('search.emails.emptyDesc')}</span>
            </div>
          )}
        </SearchSection>
      )}
    </div>
  )
}
