import { useState, useCallback } from 'react'
import { emailAPI } from '../api'

export default function EmailsPage() {
  const [mailbox, setMailbox] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [messages, setMessages] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [searched, setSearched] = useState(false)

  // Detail
  const [selected, setSelected] = useState(null)
  const [detail, setDetail] = useState(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [bodyView, setBodyView] = useState('text')

  const doSearch = useCallback(async (e) => {
    if (e) e.preventDefault()
    if (!mailbox.trim()) return
    setQuery(mailbox)
    setPage(1)
    setSelected(null)
    setDetail(null)
    setLoading(true)
    setSearched(true)
    setError(null)
    try {
      const data = await emailAPI.list(mailbox, 1, size)
      setMessages(Array.isArray(data?.messages) ? data.messages : Array.isArray(data) ? data : [])
    } catch (err) {
      setError(err.message)
      setMessages([])
    } finally {
      setLoading(false)
    }
  }, [mailbox, size])

  const loadMessage = async (msg) => {
    setSelected(msg.message_id)
    setDetailLoading(true)
    try {
      const data = await emailAPI.body(msg.message_id, query)
      setDetail(data)
    } catch (err) {
      setDetail({ _error: err.message })
    } finally {
      setDetailLoading(false)
    }
  }

  const formatDate = (v) => {
    if (!v) return '-'
    const d = new Date(v)
    return isNaN(d.getTime()) ? v : d.toLocaleString('zh-CN', { hour12: false })
  }

  const eHtml = (v) => String(v ?? '')
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h1>邮件查询</h1>
        <p style={{ color: '#888', fontSize: 13, marginTop: 4 }}>按邮箱查看结构化邮件内容，支持正文预览、详情和附件下载。</p>
      </div>

      {/* Search */}
      <div className="section" style={{ marginBottom: 16 }}>
        <form onSubmit={doSearch} style={{ display: 'grid', gridTemplateColumns: '1fr 100px 100px auto auto', gap: 8 }}>
          <input value={mailbox} onChange={e => setMailbox(e.target.value)} placeholder="输入邮箱地址，例如 order-001@asadad.bond" />
          <input type="number" value={page} onChange={e => setPage(parseInt(e.target.value) || 1)} min={1} placeholder="页码" />
          <input type="number" value={size} onChange={e => setSize(parseInt(e.target.value) || 20)} min={1} max={100} placeholder="每页条数" />
          <button className="btn btn-primary" type="submit" disabled={loading}>查询</button>
          <button className="btn btn-outline" type="button" onClick={() => { setMailbox(''); setMessages([]); setSearched(false); setDetail(null); }}>重置</button>
        </form>
      </div>

      {/* Split: list + detail */}
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(340px, 400px) minmax(0, 1fr)', gap: 16, alignItems: 'start' }}>
        {/* List */}
        <div className="section" style={{ overflow: 'hidden' }}>
          <h3>邮件列表</h3>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ minWidth: '100%' }}>
              <thead>
                <tr><th>标题</th><th>发件人</th><th>时间</th><th>附件</th></tr>
              </thead>
              <tbody>
                {loading && <tr><td colSpan={4} style={{ textAlign: 'center', color: '#888', padding: 20 }}>加载中...</td></tr>}
                {error && <tr><td colSpan={4} style={{ textAlign: 'center', padding: 20 }}><span style={{ color: '#c62828' }}>{error}</span></td></tr>}
                {!loading && !error && messages.map(msg => (
                  <tr key={msg.message_id} onClick={() => loadMessage(msg)}
                    style={{ cursor: 'pointer', background: selected === msg.message_id ? '#eef7f4' : undefined }}>
                    <td>
                      <div style={{ fontWeight: 650, marginBottom: 4 }}>{msg.subject || '(无标题)'}</div>
                      <div style={{ color: '#888', fontSize: 12, maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {msg.text_preview || '-'}
                      </div>
                    </td>
                    <td>{msg.from || '-'}</td>
                    <td>{formatDate(msg.date || msg.received_at)}</td>
                    <td>{msg.attachments_count || 0}</td>
                  </tr>
                ))}
                {!loading && !error && searched && messages.length === 0 && (
                  <tr><td colSpan={4} style={{ textAlign: 'center', color: '#888', padding: 20 }}>暂无邮件</td></tr>
                )}
                {!searched && <tr><td colSpan={4} style={{ textAlign: 'center', color: '#888', padding: 20 }}>请输入邮箱地址后查询</td></tr>}
              </tbody>
            </table>
          </div>
        </div>

        {/* Detail */}
        <div className="section">
          <h3>邮件详情</h3>
          {detailLoading && <div style={{ color: '#888', padding: 20 }}>加载中...</div>}
          {detail && detail._error && <div style={{ color: '#c62828' }}>{detail._error}</div>}
          {detail && !detail._error && (
            <div>
              <div style={{ display: 'grid', gap: 8, marginBottom: 14 }}>
                <div><strong>Message-ID：</strong><code style={{ fontSize: 11 }}>{detail.message_id || '-'}</code></div>
                <div><strong>主题：</strong>{eHtml(detail.subject || '-')}</div>
                <div><strong>发件人：</strong>{eHtml(detail.from || '-')}</div>
                <div><strong>收件人：</strong>{eHtml((detail.to || []).join(', ') || '-')}</div>
                <div><strong>时间：</strong>{detail.date || detail.received_at || '-'}</div>
                <div><strong>解析状态：</strong>{detail.parse_status || 'ok'}
                  {detail.parse_error && <span style={{ color: '#c62828', fontSize: 12, marginLeft: 8 }}>{detail.parse_error}</span>}
                </div>
              </div>

              <div style={{ display: 'inline-flex', gap: 2, padding: 3, background: '#e9ecef', borderRadius: 8, marginBottom: 12 }}>
                <button className={`btn btn-sm ${bodyView === 'text' ? 'btn-primary' : 'btn-outline'}`} style={{ borderRadius: 6 }}
                  onClick={() => setBodyView('text')}>Text</button>
                <button className={`btn btn-sm ${bodyView === 'html' ? 'btn-primary' : 'btn-outline'}`} style={{ borderRadius: 6 }}
                  onClick={() => setBodyView('html')}>HTML</button>
              </div>

              {bodyView === 'text' && (
                <div style={{ background: '#fbfbfc', border: '1px solid #eee', borderRadius: 8, padding: 12, marginBottom: 14 }}>
                  <h4 style={{ fontSize: 13, marginBottom: 8 }}>正文</h4>
                  <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontFamily: 'monospace', fontSize: 12, lineHeight: 1.55, color: '#334155', margin: 0 }}>
                    {detail.text_body || '-'}
                  </pre>
                </div>
              )}
              {bodyView === 'html' && (
                <div style={{ background: '#fbfbfc', border: '1px solid #eee', borderRadius: 8, padding: 12, marginBottom: 14 }}>
                  <h4 style={{ fontSize: 13, marginBottom: 8 }}>HTML</h4>
                  <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontFamily: 'monospace', fontSize: 12, lineHeight: 1.55, color: '#334155', margin: 0 }}>
                    {detail.html_body || '-'}
                  </pre>
                </div>
              )}

              <div style={{ background: '#fbfbfc', border: '1px solid #eee', borderRadius: 8, padding: 12 }}>
                <h4 style={{ fontSize: 13, marginBottom: 8 }}>附件</h4>
                {detail.attachments && detail.attachments.length > 0 ? (
                  <div style={{ display: 'grid', gap: 8 }}>
                    {detail.attachments.map(a => (
                      <div key={a.index} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 10px', border: '1px solid #e2e6e8', borderRadius: 6, background: '#fff' }}>
                        <div>
                          <div style={{ fontWeight: 650, fontSize: 13 }}>{a.filename || `attachment-${a.index}`}</div>
                          <div style={{ color: '#888', fontSize: 12 }}>{a.content_type || '-'} · {a.size || 0} bytes · {a.disposition || '-'}</div>
                        </div>
                        <a href={emailAPI.attachmentUrl(detail.message_id, a.index, query)}
                          download={a.filename || `attachment-${a.index}`}
                          style={{ padding: '4px 12px', border: '1px solid #d0d7de', borderRadius: 4, textDecoration: 'none', color: '#0969da', fontSize: 12 }}>
                          下载
                        </a>
                      </div>
                    ))}
                  </div>
                ) : <div style={{ color: '#888', fontSize: 13 }}>无附件</div>}
              </div>
            </div>
          )}
          {!detail && !detailLoading && <div style={{ color: '#888', fontSize: 13, padding: 20 }}>请选择一封邮件。</div>}
        </div>
      </div>
    </div>
  )
}
