import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import {
  Copy,
  Download,
  Eye,
  FileText,
  Inbox,
  MailOpen,
  Paperclip,
  RefreshCw,
  Search,
  ShieldCheck,
  X,
} from 'lucide-react'
import { emailAPI } from '../api'

function normalizeContentID(value) {
  let normalized = String(value || '').trim()
  normalized = normalized.replace(/^cid:/i, '').trim()
  normalized = normalized.replace(/^<+|>+$/g, '').trim()
  try {
    normalized = decodeURIComponent(normalized)
  } catch {
    // Keep the original value when a mail client emits invalid percent encoding.
  }
  return normalized.replace(/^<+|>+$/g, '').trim().toLowerCase()
}

function buildCidAttachmentMap(detail, mailbox) {
  const cidMap = new Map()
  for (const attachment of detail?.attachments || []) {
    if (!attachment?.inline || !attachment.content_id) continue
    const cid = normalizeContentID(attachment.content_id)
    if (!cid) continue
    cidMap.set(cid, emailAPI.attachmentUrl(detail.message_id, attachment.index, mailbox))
  }
  return cidMap
}

function isDangerousURL(value) {
  return /^\s*(javascript|vbscript|file):/i.test(String(value || ''))
}

function sanitizeURLAttributes(doc) {
  for (const element of Array.from(doc.querySelectorAll('*'))) {
    for (const attr of Array.from(element.attributes)) {
      const name = attr.name.toLowerCase()
      if (name.startsWith('on')) {
        element.removeAttribute(attr.name)
        continue
      }
      if ((name === 'href' || name === 'src' || name === 'action' || name === 'xlink:href') && isDangerousURL(attr.value)) {
        element.removeAttribute(attr.name)
      }
    }
  }
}

function buildSafeEmailHtml(detail, mailbox) {
  const html = String(detail?.html_body || '').trim()
  if (!html) return ''

  const parser = new DOMParser()
  const doc = parser.parseFromString(html, 'text/html')
  const cidMap = buildCidAttachmentMap(detail, mailbox)

  for (const element of Array.from(doc.querySelectorAll('script, iframe, object, embed, applet, form'))) {
    element.remove()
  }
  sanitizeURLAttributes(doc)

  for (const img of Array.from(doc.querySelectorAll('img'))) {
    const src = img.getAttribute('src') || ''
    if (/^\s*cid:/i.test(src)) {
      const attachmentURL = cidMap.get(normalizeContentID(src))
      if (attachmentURL) {
        img.setAttribute('src', attachmentURL)
      } else {
        img.removeAttribute('src')
        img.setAttribute('alt', img.getAttribute('alt') || 'inline image not found')
      }
    } else if (!/^\s*data:image\//i.test(src)) {
      img.removeAttribute('src')
    }
    img.removeAttribute('srcset')
  }

  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src 'self' data: blob:; style-src 'unsafe-inline'; font-src data:; base-uri 'none'; form-action 'none'">
<style>
  body { margin: 0; padding: 12px; color: #334155; font: 14px/1.55 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #fff; }
  img { max-width: 100%; height: auto; }
  table { max-width: 100%; }
</style>
</head>
<body>${doc.body.innerHTML}</body>
</html>`
}

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function formatBytes(value) {
  const size = Number(value) || 0
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(1)} MB`
}

function normalizeContentType(value) {
  return String(value || '').split(';')[0].trim().toLowerCase()
}

function isPreviewableAttachment(attachment) {
  const type = normalizeContentType(attachment?.content_type)
  return (type.startsWith('image/') && type !== 'image/svg+xml')
    || type.startsWith('text/')
    || type === 'application/pdf'
    || type === 'application/json'
    || type === 'application/xml'
    || type === 'application/xhtml+xml'
    || type.endsWith('+json')
    || type.endsWith('+xml')
}

async function readPreviewError(resp) {
  const contentType = resp.headers.get('content-type') || ''
  try {
    if (contentType.includes('application/json')) {
      const data = await resp.json()
      return data.message || `HTTP ${resp.status}`
    }
    const text = await resp.text()
    return text || `HTTP ${resp.status}`
  } catch {
    return `HTTP ${resp.status}`
  }
}

function AttachmentPreviewModal({ preview, onClose, onCopy }) {
  if (!preview) return null
  const name = preview.attachment?.filename || `attachment-${preview.attachment?.index}`
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal attachment-preview-modal" onClick={e => e.stopPropagation()} role="dialog" aria-modal="true" aria-label="附件预览">
        <div className="attachment-preview-head">
          <div>
            <div className="drawer-kicker">Attachment preview</div>
            <h3 title={name}>{name}</h3>
            <p>{preview.contentType || preview.attachment?.content_type || '-'} · {formatBytes(preview.attachment?.size)}</p>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="关闭预览">
            <X size={18} />
          </button>
        </div>

        <div className="attachment-preview-body">
          {preview.loading && <div className="empty-state"><span className="spinner" /><strong>加载预览...</strong></div>}
          {preview.error && (
            <div className="empty-state error-state">
              <Paperclip size={28} />
              <strong>无法预览</strong>
              <span>{preview.error}</span>
              <a className="btn btn-sm btn-outline" href={preview.downloadUrl} download={name}>
                <Download size={14} /> 下载附件
              </a>
            </div>
          )}
          {!preview.loading && !preview.error && preview.kind === 'image' && (
            <div className="attachment-preview-frame image-frame">
              <img src={preview.objectUrl} alt={name} />
            </div>
          )}
          {!preview.loading && !preview.error && preview.kind === 'pdf' && (
            <iframe className="attachment-preview-frame pdf-frame" title={name} src={preview.objectUrl} />
          )}
          {!preview.loading && !preview.error && preview.kind === 'text' && (
            <div className="attachment-preview-text">
              <div className="attachment-preview-actions">
                <button className="btn btn-sm btn-outline" type="button" onClick={() => onCopy(preview.text || '')}>
                  <Copy size={14} /> 复制
                </button>
              </div>
              <pre>{preview.text || ''}</pre>
            </div>
          )}
        </div>

        <div className="modal-footer">
          <a className="btn btn-outline" href={preview.downloadUrl} download={name}>
            <Download size={15} /> 下载
          </a>
          <button className="btn btn-primary" type="button" onClick={onClose}>关闭</button>
        </div>
      </div>
    </div>
  )
}

export default function EmailsPage() {
  const location = useLocation()
  const appliedURLSearch = useRef('')
  const [mailbox, setMailbox] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [messages, setMessages] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [searched, setSearched] = useState(false)

  const [selected, setSelected] = useState(null)
  const [detail, setDetail] = useState(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [bodyView, setBodyView] = useState('text')
  const [attachmentPreview, setAttachmentPreview] = useState(null)

  const totalAttachments = useMemo(
    () => messages.reduce((sum, msg) => sum + (Number(msg.attachments_count) || 0), 0),
    [messages],
  )

  const fetchMessages = useCallback(async (targetMailbox, targetPage = 1, targetSize = size) => {
    const normalizedMailbox = String(targetMailbox || '').trim()
    if (!normalizedMailbox) return
    setLoading(true)
    setSearched(true)
    setError(null)
    setQuery(normalizedMailbox)
    setSelected(null)
    setDetail(null)
    try {
      const data = await emailAPI.list(normalizedMailbox, targetPage, targetSize)
      setMessages(Array.isArray(data?.messages) ? data.messages : Array.isArray(data) ? data : [])
      setPage(targetPage)
    } catch (err) {
      setError(err.message)
      setMessages([])
    } finally {
      setLoading(false)
    }
  }, [size])

  const fetchMessageByID = useCallback(async (targetMailbox, messageID) => {
    const normalizedMailbox = String(targetMailbox || '').trim()
    const normalizedMessageID = String(messageID || '').trim()
    if (!normalizedMailbox || !normalizedMessageID) return
    setLoading(false)
    setSearched(true)
    setError(null)
    setMailbox(normalizedMailbox)
    setQuery(normalizedMailbox)
    setMessages([])
    setSelected(normalizedMessageID)
    setDetail(null)
    setDetailLoading(true)
    setBodyView('text')
    try {
      const data = await emailAPI.body(normalizedMessageID, normalizedMailbox)
      setDetail(data)
      setBodyView(data?.text_body ? 'text' : data?.html_body ? 'html' : 'meta')
    } catch (err) {
      setDetail({ _error: err.message })
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    if (appliedURLSearch.current === location.search) return
    appliedURLSearch.current = location.search
    const params = new URLSearchParams(location.search)
    const mailboxParam = params.get('mailbox') || ''
    const messageID = params.get('message_id') || ''

    if (mailboxParam && messageID) {
      fetchMessageByID(mailboxParam, messageID)
      return
    }
    if (mailboxParam) {
      setMailbox(mailboxParam)
      fetchMessages(mailboxParam, 1, size)
      return
    }
    if (messageID) {
      setSearched(true)
      setError('Message-ID 查询需要同时提供邮箱地址。请先输入邮箱地址查询邮件，再打开对应邮件详情。')
      setMessages([])
      setSelected(messageID)
      setDetail(null)
    }
  }, [fetchMessageByID, fetchMessages, location.search, size])

  useEffect(() => () => {
    if (attachmentPreview?.objectUrl) {
      URL.revokeObjectURL(attachmentPreview.objectUrl)
    }
  }, [attachmentPreview?.objectUrl])

  const doSearch = (e) => {
    if (e) e.preventDefault()
    fetchMessages(mailbox, 1, size)
  }

  const loadMessage = async (msg) => {
    setSelected(msg.message_id)
    setDetailLoading(true)
    setBodyView(msg.html_body ? 'html' : 'text')
    try {
      const data = await emailAPI.body(msg.message_id, query)
      setDetail(data)
      setBodyView(data?.text_body ? 'text' : data?.html_body ? 'html' : 'meta')
    } catch (err) {
      setDetail({ _error: err.message })
    } finally {
      setDetailLoading(false)
    }
  }

  const reset = () => {
    setMailbox('')
    setQuery('')
    setPage(1)
    setMessages([])
    setSearched(false)
    setError(null)
    setSelected(null)
    setDetail(null)
    setAttachmentPreview(null)
  }

  const closeAttachmentPreview = () => {
    setAttachmentPreview(null)
  }

  const openAttachmentPreview = async (attachment) => {
    if (!detail?.message_id) return
    const previewUrl = emailAPI.attachmentPreviewUrl(detail.message_id, attachment.index, query)
    const downloadUrl = emailAPI.attachmentUrl(detail.message_id, attachment.index, query)
    setAttachmentPreview({ attachment, downloadUrl, loading: true })
    try {
      const resp = await fetch(previewUrl)
      if (!resp.ok) {
        throw new Error(await readPreviewError(resp))
      }
      const contentType = normalizeContentType(resp.headers.get('content-type') || attachment.content_type)
      const blob = await resp.blob()
      if (contentType.startsWith('image/')) {
        setAttachmentPreview({ attachment, downloadUrl, loading: false, kind: 'image', objectUrl: URL.createObjectURL(blob), contentType })
        return
      }
      if (contentType === 'application/pdf') {
        setAttachmentPreview({ attachment, downloadUrl, loading: false, kind: 'pdf', objectUrl: URL.createObjectURL(blob), contentType })
        return
      }
      setAttachmentPreview({ attachment, downloadUrl, loading: false, kind: 'text', text: await blob.text(), contentType })
    } catch (err) {
      setAttachmentPreview({ attachment, downloadUrl, loading: false, error: err.message || '预览失败' })
    }
  }

  const copyPreviewText = async (text) => {
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // Clipboard availability depends on browser permissions; preview remains usable.
    }
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>邮件查询</h1>
          <p className="page-subtitle">按邮箱维度查看结构化邮件，支持正文审阅、HTML 预览和附件下载。</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => fetchMessages(query || mailbox, page, size)} disabled={loading || !(query || mailbox).trim()}>
            {loading ? <span className="spinner" /> : <RefreshCw size={16} />}
            刷新
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <div className="summary-tile" data-tone="brand">
          <span className="summary-icon"><Inbox size={18} /></span>
          <div>
            <div className="summary-value">{messages.length}</div>
            <div className="summary-label">当前页邮件</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="info">
          <span className="summary-icon"><Paperclip size={18} /></span>
          <div>
            <div className="summary-value">{totalAttachments}</div>
            <div className="summary-label">附件入口</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="success">
          <span className="summary-icon"><ShieldCheck size={18} /></span>
          <div>
            <div className="summary-value">{detail?.parse_status || (detail ? 'ok' : '-')}</div>
            <div className="summary-label">解析状态</div>
          </div>
        </div>
      </div>

      <section className="section email-search-panel">
        <form className="email-search-form" onSubmit={doSearch}>
          <div className="search-input-wrap">
            <Search size={17} />
            <input value={mailbox} onChange={e => setMailbox(e.target.value)} placeholder="输入邮箱地址，例如 order-001@example.com" />
          </div>
          <input type="number" value={size} onChange={e => setSize(parseInt(e.target.value, 10) || 20)} min={1} max={100} aria-label="每页条数" />
          <button className="btn btn-primary" type="submit" disabled={loading || !mailbox.trim()}>
            {loading && <span className="spinner" />} 查询
          </button>
          <button className="btn btn-outline" type="button" onClick={reset}>重置</button>
        </form>
      </section>

      <div className="email-workbench">
        <section className="section email-list-panel">
          <div className="panel-header">
            <div>
              <h3>邮件列表</h3>
              <div className="panel-caption" title={query || undefined}>{query ? query : '等待输入邮箱地址'}</div>
            </div>
          </div>

          <div className="email-list">
            {loading && (
              <div className="empty-state"><span className="spinner" /><strong>加载邮件...</strong></div>
            )}
            {error && (
              <div className="empty-state error-state"><MailOpen size={28} /><strong>查询失败</strong><span>{error}</span></div>
            )}
            {!loading && !error && messages.map(msg => (
              <button
                className={`email-list-item ${selected === msg.message_id ? 'active' : ''}`}
                key={msg.message_id}
                type="button"
                onClick={() => loadMessage(msg)}
              >
                <div className="email-list-top">
                  <strong title={msg.subject || '(无标题)'}>{msg.subject || '(无标题)'}</strong>
                  {(msg.attachments_count || 0) > 0 && <span className="tag tag-info"><Paperclip size={12} /> {msg.attachments_count}</span>}
                </div>
                <div className="email-list-meta">
                  <span title={msg.from || '-'}>{msg.from || '-'}</span>
                  <span title={formatDate(msg.date || msg.received_at)}>{formatDate(msg.date || msg.received_at)}</span>
                </div>
                <p>{msg.text_preview || '无正文预览'}</p>
              </button>
            ))}
            {!loading && !error && searched && messages.length === 0 && (
              <div className="empty-state"><Inbox size={28} /><strong>暂无邮件</strong><span>该邮箱当前没有可展示的结构化邮件。</span></div>
            )}
            {!searched && (
              <div className="empty-state"><Search size={28} /><strong>输入邮箱后查询</strong><span>查询结果会以紧凑列表展示，点击即可审阅详情。</span></div>
            )}
          </div>

          {searched && !loading && (
            <div className="pagination-bar email-pagination">
              <button className="btn btn-sm btn-outline" disabled={page <= 1} onClick={() => fetchMessages(query, page - 1, size)}>上一页</button>
              <span>第 <strong>{page}</strong> 页</span>
              <button className="btn btn-sm btn-outline" disabled={messages.length < size} onClick={() => fetchMessages(query, page + 1, size)}>下一页</button>
            </div>
          )}
        </section>

        <section className="section email-detail-panel">
          <div className="panel-header">
            <div>
              <h3>邮件详情</h3>
              <div className="panel-caption">正文、HTML 和附件在同一审阅器中处理。</div>
            </div>
            {detail && !detail._error && <span className="tag tag-success">已解析</span>}
          </div>

          {detailLoading && <div className="empty-state"><span className="spinner" /><strong>加载详情...</strong></div>}
          {detail && detail._error && <div className="empty-state error-state"><MailOpen size={28} /><strong>详情加载失败</strong><span>{detail._error}</span></div>}
          {detail && !detail._error && (
            <div className="email-detail">
              <div className="email-detail-head">
                <h2 title={detail.subject || '(无标题)'}>{detail.subject || '(无标题)'}</h2>
                <div className="email-meta-grid">
                  <span>Message-ID</span><code title={detail.message_id || '-'}>{detail.message_id || '-'}</code>
                  <span>发件人</span><strong title={detail.from || '-'}>{detail.from || '-'}</strong>
                  <span>收件人</span><strong title={(detail.to || []).join(', ') || '-'}>{(detail.to || []).join(', ') || '-'}</strong>
                  <span>时间</span><strong>{formatDate(detail.date || detail.received_at)}</strong>
                  <span>解析</span>
                  <strong>
                    {detail.parse_status || 'ok'}
                    {detail.parse_error && <em className="parse-error">{detail.parse_error}</em>}
                  </strong>
                </div>
              </div>

              <div className="phase-tabs email-body-tabs">
                {[
                  ['text', 'Text'],
                  ['html', 'HTML'],
                  ['meta', 'Raw 元信息'],
                ].map(([value, label]) => (
                  <button className={bodyView === value ? 'active' : ''} type="button" key={value} onClick={() => setBodyView(value)}>
                    {label}
                  </button>
                ))}
              </div>

              {bodyView === 'text' && (
                <div className="email-body-card">
                  <div className="body-card-title"><FileText size={15} /> 正文</div>
                  <pre>{detail.text_body || '-'}</pre>
                </div>
              )}
              {bodyView === 'html' && (
                <div className="email-body-card">
                  <div className="body-card-title"><ShieldCheck size={15} /> HTML 预览</div>
                  {detail.html_body ? (
                    <iframe
                      title="邮件 HTML 预览"
                      sandbox="allow-same-origin"
                      srcDoc={buildSafeEmailHtml(detail, query)}
                    />
                  ) : <div className="muted-text">无 HTML 正文</div>}
                </div>
              )}
              {bodyView === 'meta' && (
                <div className="email-body-card">
                  <div className="body-card-title"><FileText size={15} /> 元信息</div>
                  <pre>{JSON.stringify({
                    message_id: detail.message_id,
                    from: detail.from,
                    to: detail.to,
                    date: detail.date || detail.received_at,
                    parse_status: detail.parse_status || 'ok',
                    attachments: detail.attachments || [],
                  }, null, 2)}</pre>
                </div>
              )}

              <div className="attachments-panel">
                <div className="body-card-title"><Paperclip size={15} /> 附件</div>
                {detail.attachments && detail.attachments.length > 0 ? (
                  <div className="attachment-list">
                    {detail.attachments.map(a => (
                      <div className="attachment-item" key={a.index}>
                        <div className="attachment-copy">
                          <strong title={a.filename || `attachment-${a.index}`}>{a.filename || `attachment-${a.index}`}</strong>
                          <span title={`${a.content_type || '-'} · ${formatBytes(a.size)} · ${a.inline ? 'inline' : (a.disposition || 'attachment')}`}>
                            {a.content_type || '-'} · {formatBytes(a.size)} · {a.inline ? 'inline' : (a.disposition || 'attachment')}
                          </span>
                        </div>
                        <div className="attachment-actions">
                          {isPreviewableAttachment(a) && (
                            <button className="btn btn-sm btn-outline" type="button" onClick={() => openAttachmentPreview(a)}>
                              <Eye size={14} /> 预览
                            </button>
                          )}
                          <a className="btn btn-sm btn-outline" href={emailAPI.attachmentUrl(detail.message_id, a.index, query)} download={a.filename || `attachment-${a.index}`}>
                            <Download size={14} /> 下载
                          </a>
                        </div>
                      </div>
                    ))}
                  </div>
                ) : <div className="muted-text">无附件</div>}
              </div>
            </div>
          )}
          {!detail && !detailLoading && (
            <div className="empty-state"><MailOpen size={28} /><strong>选择一封邮件</strong><span>详情会展示主题、收发件人、正文和附件。</span></div>
          )}
        </section>
      </div>
      <AttachmentPreviewModal preview={attachmentPreview} onClose={closeAttachmentPreview} onCopy={copyPreviewText} />
    </div>
  )
}
