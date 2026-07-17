import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
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
  Trash2,
  X,
} from 'lucide-react'
import { emailAPI } from '../api'
import { formatDateTime } from '../i18n'

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
  if (type === 'image/svg+xml') return false
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
  const { t } = useTranslation('pages')
  if (!preview) return null
  const name = preview.attachment?.filename || `attachment-${preview.attachment?.index}`
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal attachment-preview-modal" onClick={e => e.stopPropagation()} role="dialog" aria-modal="true" aria-label={t('emails.preview.aria')}>
        <div className="attachment-preview-head">
          <div>
            <div className="drawer-kicker">Attachment preview</div>
            <h3 title={name}>{name}</h3>
            <p>{preview.contentType || preview.attachment?.content_type || '-'} · {formatBytes(preview.attachment?.size)}</p>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label={t('emails.preview.closeAria')}>
            <X size={18} />
          </button>
        </div>

        <div className="attachment-preview-body">
          {preview.loading && <div className="empty-state"><span className="spinner" /><strong>{t('emails.preview.loading')}</strong></div>}
          {preview.error && (
            <div className="empty-state error-state">
              <Paperclip size={28} />
              <strong>{t('emails.preview.failed')}</strong>
              <span>{preview.error}</span>
              <a className="btn btn-sm btn-outline" href={preview.downloadUrl} download={name}>
                <Download size={14} /> {t('emails.preview.downloadAttachment')}
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
                  <Copy size={14} /> {t('emails.preview.copy')}
                </button>
              </div>
              <pre>{preview.text || ''}</pre>
            </div>
          )}
        </div>

        <div className="modal-footer">
          <a className="btn btn-outline" href={preview.downloadUrl} download={name}>
            <Download size={15} /> {t('emails.preview.download')}
          </a>
          <button className="btn btn-primary" type="button" onClick={onClose}>{t('common:actions.close')}</button>
        </div>
      </div>
    </div>
  )
}

function DeleteMessageDialog({ message, mailbox, deleting, onConfirm, onCancel }) {
  const { t } = useTranslation('pages')
  if (!message) return null
  return (
    <div className="modal-overlay" onClick={deleting ? undefined : onCancel}>
      <div className="modal confirm-modal" onClick={e => e.stopPropagation()} role="dialog" aria-modal="true" aria-label={t('emails.deleteDialog.aria')}>
        <h3>{t('emails.deleteDialog.title')}</h3>
        <p>{t('emails.deleteDialog.desc')}</p>
        <div className="email-meta-grid">
          <span>{t('emails.deleteDialog.subject')}</span><strong>{message.subject || t('emails.list.noSubject')}</strong>
          <span>{t('emails.deleteDialog.mailbox')}</span><code>{mailbox}</code>
          <span>Message-ID</span><code>{message.message_id}</code>
        </div>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onCancel} disabled={deleting}>{t('common:actions.cancel')}</button>
          <button className="btn btn-danger" type="button" onClick={onConfirm} disabled={deleting}>
            {deleting ? <span className="spinner" /> : <Trash2 size={15} />} {t('emails.deleteDialog.confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}

export default function EmailsPage() {
  const { t } = useTranslation('pages')
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
  const [deleteConfirm, setDeleteConfirm] = useState(null)
  const [deleting, setDeleting] = useState(false)

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
      setError(t('emails.errors.messageIdNeedsMailbox'))
      setMessages([])
      setSelected(messageID)
      setDetail(null)
    }
  }, [fetchMessageByID, fetchMessages, location.search, size, t])

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
      setAttachmentPreview({ attachment, downloadUrl, loading: false, error: err.message || t('emails.preview.failedFallback') })
    }
  }

  const copyPreviewText = async (text) => {
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // Clipboard availability depends on browser permissions; preview remains usable.
    }
  }

  const deleteSelectedMessage = async () => {
    if (!deleteConfirm?.message_id || !query) return
    setDeleting(true)
    setError(null)
    try {
      await emailAPI.remove(deleteConfirm.message_id, query)
      setMessages(current => current.filter(message => message.message_id !== deleteConfirm.message_id))
      setSelected(null)
      setDetail(null)
      setAttachmentPreview(null)
      setDeleteConfirm(null)
    } catch (err) {
      setError(err.message || t('emails.errors.deleteFailed'))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('emails.title')}</h1>
          <p className="page-subtitle">{t('emails.subtitle')}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => fetchMessages(query || mailbox, page, size)} disabled={loading || !(query || mailbox).trim()}>
            {loading ? <span className="spinner" /> : <RefreshCw size={16} />}
            {t('common:actions.refresh')}
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <div className="summary-tile" data-tone="brand">
          <span className="summary-icon"><Inbox size={18} /></span>
          <div>
            <div className="summary-value">{messages.length}</div>
            <div className="summary-label">{t('emails.summary.current')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="info">
          <span className="summary-icon"><Paperclip size={18} /></span>
          <div>
            <div className="summary-value">{totalAttachments}</div>
            <div className="summary-label">{t('emails.summary.attachments')}</div>
          </div>
        </div>
        <div className="summary-tile" data-tone="success">
          <span className="summary-icon"><ShieldCheck size={18} /></span>
          <div>
            <div className="summary-value">{detail?.parse_status || (detail ? 'ok' : '-')}</div>
            <div className="summary-label">{t('emails.summary.parseStatus')}</div>
          </div>
        </div>
      </div>

      <section className="section email-search-panel">
        <form className="email-search-form" onSubmit={doSearch}>
          <div className="search-input-wrap">
            <Search size={17} />
            <input value={mailbox} onChange={e => setMailbox(e.target.value)} placeholder={t('emails.search.placeholder')} />
          </div>
          <input type="number" value={size} onChange={e => setSize(parseInt(e.target.value, 10) || 20)} min={1} max={100} aria-label={t('emails.search.pageSize')} />
          <button className="btn btn-primary" type="submit" disabled={loading || !mailbox.trim()}>
            {loading && <span className="spinner" />} {t('emails.search.submit')}
          </button>
          <button className="btn btn-outline" type="button" onClick={reset}>{t('emails.search.reset')}</button>
        </form>
      </section>

      <div className="email-workbench">
        <section className="section email-list-panel">
          <div className="panel-header">
            <div>
              <h3>{t('emails.list.title')}</h3>
              <div className="panel-caption" title={query || undefined}>{query ? query : t('emails.list.waiting')}</div>
            </div>
          </div>

          <div className="email-list">
            {loading && (
              <div className="empty-state"><span className="spinner" /><strong>{t('emails.list.loading')}</strong></div>
            )}
            {error && (
              <div className="empty-state error-state"><MailOpen size={28} /><strong>{t('emails.list.failed')}</strong><span>{error}</span></div>
            )}
            {!loading && !error && messages.map(msg => (
              <button
                className={`email-list-item ${selected === msg.message_id ? 'active' : ''}`}
                key={msg.message_id}
                type="button"
                onClick={() => loadMessage(msg)}
              >
                <div className="email-list-top">
                  <strong title={msg.subject || t('emails.list.noSubject')}>{msg.subject || t('emails.list.noSubject')}</strong>
                  {(msg.attachments_count || 0) > 0 && <span className="tag tag-info"><Paperclip size={12} /> {msg.attachments_count}</span>}
                </div>
                <div className="email-list-meta">
                  <span title={msg.from || '-'}>{msg.from || '-'}</span>
                  <span title={formatDateTime(msg.date || msg.received_at)}>{formatDateTime(msg.date || msg.received_at)}</span>
                </div>
                <p>{msg.text_preview || t('emails.list.noPreview')}</p>
              </button>
            ))}
            {!loading && !error && searched && messages.length === 0 && (
              <div className="empty-state"><Inbox size={28} /><strong>{t('emails.list.empty')}</strong><span>{t('emails.list.emptyDesc')}</span></div>
            )}
            {!searched && (
              <div className="empty-state"><Search size={28} /><strong>{t('emails.list.initial')}</strong><span>{t('emails.list.initialDesc')}</span></div>
            )}
          </div>

          {searched && !loading && (
            <div className="pagination-bar email-pagination">
              <button className="btn btn-sm btn-outline" disabled={page <= 1} onClick={() => fetchMessages(query, page - 1, size)}>{t('emails.list.previous')}</button>
              <span>{t('emails.list.page', { page })}</span>
              <button className="btn btn-sm btn-outline" disabled={messages.length < size} onClick={() => fetchMessages(query, page + 1, size)}>{t('emails.list.next')}</button>
            </div>
          )}
        </section>

        <section className="section email-detail-panel">
          <div className="panel-header">
            <div>
              <h3>{t('emails.detail.title')}</h3>
              <div className="panel-caption">{t('emails.detail.caption')}</div>
            </div>
            {detail && !detail._error && (
              <div className="page-actions">
                <span className="tag tag-success">{t('emails.detail.parsed')}</span>
                <button className="btn btn-sm btn-danger" type="button" onClick={() => setDeleteConfirm(detail)}>
                  <Trash2 size={14} /> {t('emails.detail.delete')}
                </button>
              </div>
            )}
          </div>

          {detailLoading && <div className="empty-state"><span className="spinner" /><strong>{t('emails.detail.loading')}</strong></div>}
          {detail && detail._error && <div className="empty-state error-state"><MailOpen size={28} /><strong>{t('emails.detail.failed')}</strong><span>{detail._error}</span></div>}
          {detail && !detail._error && (
            <div className="email-detail">
              <div className="email-detail-head">
                <h2 title={detail.subject || t('emails.list.noSubject')}>{detail.subject || t('emails.list.noSubject')}</h2>
                <div className="email-meta-grid">
                  <span>Message-ID</span><code title={detail.message_id || '-'}>{detail.message_id || '-'}</code>
                  <span>{t('emails.detail.sender')}</span><strong title={detail.from || '-'}>{detail.from || '-'}</strong>
                  <span>{t('emails.detail.recipient')}</span><strong title={(detail.to || []).join(', ') || '-'}>{(detail.to || []).join(', ') || '-'}</strong>
                  <span>{t('emails.detail.time')}</span><strong>{formatDateTime(detail.date || detail.received_at)}</strong>
                  <span>{t('emails.detail.parse')}</span>
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
                  ['meta', t('emails.detail.rawMeta')],
                ].map(([value, label]) => (
                  <button className={bodyView === value ? 'active' : ''} type="button" key={value} onClick={() => setBodyView(value)}>
                    {label}
                  </button>
                ))}
              </div>

              {bodyView === 'text' && (
                <div className="email-body-card">
                  <div className="body-card-title"><FileText size={15} /> {t('emails.detail.body')}</div>
                  <pre>{detail.text_body || '-'}</pre>
                </div>
              )}
              {bodyView === 'html' && (
                <div className="email-body-card">
                  <div className="body-card-title"><ShieldCheck size={15} /> {t('emails.detail.htmlPreview')}</div>
                  {detail.html_body ? (
                    <iframe
                      title={t('emails.detail.htmlAria')}
                      sandbox="allow-same-origin"
                      srcDoc={buildSafeEmailHtml(detail, query)}
                    />
                  ) : <div className="muted-text">{t('emails.detail.noHtml')}</div>}
                </div>
              )}
              {bodyView === 'meta' && (
                <div className="email-body-card">
                  <div className="body-card-title"><FileText size={15} /> {t('emails.detail.metadata')}</div>
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
                <div className="body-card-title"><Paperclip size={15} /> {t('emails.detail.attachments')}</div>
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
                              <Eye size={14} /> {t('emails.detail.preview')}
                            </button>
                          )}
                          <a className="btn btn-sm btn-outline" href={emailAPI.attachmentUrl(detail.message_id, a.index, query)} download={a.filename || `attachment-${a.index}`}>
                            <Download size={14} /> {t('emails.detail.download')}
                          </a>
                        </div>
                      </div>
                    ))}
                  </div>
                ) : <div className="muted-text">{t('emails.detail.noAttachments')}</div>}
              </div>
            </div>
          )}
          {!detail && !detailLoading && (
            <div className="empty-state"><MailOpen size={28} /><strong>{t('emails.detail.select')}</strong><span>{t('emails.detail.selectDesc')}</span></div>
          )}
        </section>
      </div>
      <AttachmentPreviewModal preview={attachmentPreview} onClose={closeAttachmentPreview} onCopy={copyPreviewText} />
      <DeleteMessageDialog message={deleteConfirm} mailbox={query} deleting={deleting} onConfirm={deleteSelectedMessage} onCancel={() => setDeleteConfirm(null)} />
    </div>
  )
}
