import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity,
  Check,
  Clipboard,
  Clock3,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  ShieldCheck,
  ShieldOff,
  Trash2,
  X,
} from 'lucide-react'
import { externalAccessAPI } from '../api'

const EMPTY_FORM = {
  name: '',
  description: '',
  enabled: true,
  permission_codes: [],
  credential_name: '默认凭证',
  expires_at: '',
}

function formatDate(value, fallback = '从未') {
  if (!value) return fallback
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? fallback : date.toLocaleString('zh-CN', { hour12: false })
}

function toRFC3339(value) {
  return value ? new Date(value).toISOString() : ''
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const timer = setTimeout(onClose, 3000)
    return () => clearTimeout(timer)
  }, [onClose])
  return <div className={`toast toast-${type}`}>{message}</div>
}

function TokenDialog({ token, onClose, onCopied, onCopyError }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(token)
      onCopied()
    } catch {
      onCopyError()
    }
  }
  return (
    <div className="modal-overlay access-modal-overlay" onClick={onClose}>
      <div className="modal token-modal" onClick={event => event.stopPropagation()} role="dialog" aria-modal="true" aria-label="新 API Token">
        <div className="token-modal-heading">
          <span className="module-icon"><KeyRound size={19} /></span>
          <div><span>Credential issued</span><h3>新 API Token</h3></div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}><X size={18} /></button>
        </div>
        <div className="inline-alert token-alert">完整 Token 仅本次显示，关闭后无法再次查看。</div>
        <div className="token-secret"><code>{token}</code></div>
        <div className="modal-footer">
          <button className="btn btn-primary" type="button" onClick={copy}><Clipboard size={16} /> 复制 Token</button>
          <button className="btn btn-outline" type="button" onClick={onClose}>完成</button>
        </div>
      </div>
    </div>
  )
}

function ConfirmDialog({ title, message, confirmLabel = '确认', saving = false, onConfirm, onCancel }) {
  return (
    <div className="modal-overlay access-modal-overlay" onClick={saving ? undefined : onCancel}>
      <div className="modal confirm-modal" onClick={event => event.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" disabled={saving} onClick={onCancel}>取消</button>
          <button className="btn btn-danger" type="button" disabled={saving} onClick={onConfirm}>{saving ? '删除中...' : confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}

function PermissionSelector({ permissions, selected, onChange }) {
  const groups = useMemo(() => permissions.reduce((result, permission) => {
    const group = permission.group_name || '其他'
    result[group] = result[group] || []
    result[group].push(permission)
    return result
  }, {}), [permissions])

  const toggle = (code) => {
    onChange(selected.includes(code) ? selected.filter(item => item !== code) : [...selected, code])
  }

  return (
    <div className="permission-groups">
      {Object.entries(groups).map(([group, items]) => (
        <section className="permission-group" key={group}>
          <div className="permission-group-title">{group}</div>
          {items.map(permission => (
            <label className={`permission-option ${selected.includes(permission.code) ? 'selected' : ''}`} key={permission.code}>
              <input type="checkbox" checked={selected.includes(permission.code)} onChange={() => toggle(permission.code)} />
              <span className="permission-check"><Check size={14} /></span>
              <span><strong>{permission.name}</strong><code>{permission.code}</code></span>
            </label>
          ))}
        </section>
      ))}
    </div>
  )
}

function ApplicationDrawer({ state, permissions, saving, logs, onChange, onSave, onClose, onIssue, onDelete }) {
  const { mode, form, application } = state
  const update = (field, value) => onChange({ ...state, form: { ...form, [field]: value } })

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer external-access-drawer" onClick={event => event.stopPropagation()} aria-label={mode === 'create' ? '新增外部访问' : '编辑外部访问'}>
        <div className="drawer-header">
          <div className="drawer-title-with-icon">
            <span className="module-icon"><ShieldCheck size={18} /></span>
            <div><div className="drawer-kicker">External access</div><h2>{mode === 'create' ? '新增外部访问' : form.name}</h2></div>
          </div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}><X size={18} /></button>
        </div>

        <form className="drawer-body external-access-form" onSubmit={onSave}>
          <div className="form-group">
            <label>访问名称</label>
            <input value={form.name} onChange={event => update('name', event.target.value)} placeholder="例如：出票中心" required maxLength={128} />
          </div>
          <div className="form-group">
            <label>说明</label>
            <textarea rows={3} value={form.description} onChange={event => update('description', event.target.value)} placeholder="业务用途、负责人或环境" maxLength={512} />
          </div>
          <div className="form-group">
            <label>状态</label>
            <label className="switch-row">
              <span className="toggle"><input type="checkbox" checked={form.enabled} onChange={event => update('enabled', event.target.checked)} /><span className="toggle-slider" /></span>
              {form.enabled ? '启用' : '停用'}
            </label>
          </div>
          <div className="form-group">
            <label>可调用功能</label>
            <PermissionSelector permissions={permissions} selected={form.permission_codes} onChange={value => update('permission_codes', value)} />
          </div>

          {mode === 'create' && (
            <div className="credential-issue-fields">
              <div className="form-group"><label>凭证名称</label><input value={form.credential_name} onChange={event => update('credential_name', event.target.value)} required /></div>
              <div className="form-group"><label>到期时间</label><input type="datetime-local" value={form.expires_at} onChange={event => update('expires_at', event.target.value)} /></div>
            </div>
          )}

          {mode === 'edit' && (
            <>
              <section className="access-detail-section">
                <div className="access-detail-heading"><div><h3>API 凭证</h3><span>{application.credentials?.length || 0} 个凭证</span></div><button className="btn btn-outline btn-sm" type="button" onClick={onIssue}><Plus size={15} /> 签发并复制 Token</button></div>
                <div className="credential-list">
                  {(application.credentials || []).map(credential => (
                    <div className="credential-row" key={credential.id}>
                      <span className={`credential-state ${credential.enabled ? 'active' : ''}`}><KeyRound size={15} /></span>
                      <div><strong>{credential.name}</strong><code>{credential.token_prefix}...</code><small>最近使用：{formatDate(credential.last_used_at)}</small></div>
                      <button className="icon-button compact danger" type="button" title="永久删除凭证" onClick={() => onDelete(credential)}><Trash2 size={15} /></button>
                    </div>
                  ))}
                </div>
              </section>
              <section className="access-detail-section">
                <div className="access-detail-heading"><div><h3>最近调用</h3><span>{logs.total || 0} 条记录</span></div></div>
                <div className="access-log-list">
                  {(logs.items || []).slice(0, 8).map(log => (
                    <div className="access-log-row" key={log.id}>
                      <span className={log.status_code < 400 ? 'log-ok' : 'log-error'}>{log.status_code}</span>
                      <div><strong>{log.method} {log.path}</strong><small>{log.permission_code} · {log.client_ip || '未知 IP'} · {log.duration_ms} ms</small></div>
                      <time>{formatDate(log.created_at)}</time>
                    </div>
                  ))}
                  {(!logs.items || logs.items.length === 0) && <div className="compact-empty">暂无调用记录</div>}
                </div>
              </section>
            </>
          )}

          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>取消</button>
            <button className="btn btn-primary" type="submit" disabled={saving || form.permission_codes.length === 0}>{saving && <span className="spinner" />} 保存</button>
          </div>
        </form>
      </aside>
    </div>
  )
}

function CredentialDialog({ saving, onSave, onClose }) {
  const [form, setForm] = useState({ name: '生产凭证', expires_at: '' })
  return (
    <div className="modal-overlay access-modal-overlay" onClick={onClose}>
      <form className="modal credential-modal" onClick={event => event.stopPropagation()} onSubmit={event => { event.preventDefault(); onSave({ name: form.name, expires_at: toRFC3339(form.expires_at) }) }}>
        <h3>签发新凭证</h3>
        <div className="form-group"><label>凭证名称</label><input value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} required /></div>
        <div className="form-group"><label>到期时间</label><input type="datetime-local" value={form.expires_at} onChange={event => setForm({ ...form, expires_at: event.target.value })} /></div>
        <div className="modal-footer"><button className="btn btn-outline" type="button" onClick={onClose}>取消</button><button className="btn btn-primary" type="submit" disabled={saving}>{saving && <span className="spinner" />} 签发并复制</button></div>
      </form>
    </div>
  )
}

export default function ExternalAccessPage() {
  const [applications, setApplications] = useState([])
  const [permissions, setPermissions] = useState([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [drawer, setDrawer] = useState(null)
  const [logs, setLogs] = useState({ items: [], total: 0 })
  const [credentialDialog, setCredentialDialog] = useState(false)
  const [token, setToken] = useState('')
  const [confirm, setConfirm] = useState(null)
  const [toast, setToast] = useState(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const [apps, permissionList] = await Promise.all([externalAccessAPI.list(), externalAccessAPI.permissions()])
      setApplications(Array.isArray(apps) ? apps : [])
      setPermissions(Array.isArray(permissionList) ? permissionList : [])
    } catch (error) {
      setToast({ type: 'error', message: '加载失败: ' + error.message })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => ({
    total: applications.length,
    enabled: applications.filter(item => item.enabled).length,
    credentials: applications.reduce((sum, item) => sum + (item.credentials || []).filter(value => value.enabled).length, 0),
    used: applications.filter(item => item.last_used_at).length,
  }), [applications])

  const openCreate = () => setDrawer({ mode: 'create', form: { ...EMPTY_FORM }, application: null })

  const openEdit = async (application) => {
    try {
      const [detail, accessLogs] = await Promise.all([externalAccessAPI.get(application.id), externalAccessAPI.logs(application.id)])
      setLogs(accessLogs || { items: [], total: 0 })
      setDrawer({
        mode: 'edit',
        application: detail,
        form: { name: detail.name, description: detail.description || '', enabled: detail.enabled, permission_codes: detail.permission_codes || [] },
      })
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    }
  }

  const refreshOpenApplication = async (id) => {
    const [detail, accessLogs] = await Promise.all([externalAccessAPI.get(id), externalAccessAPI.logs(id)])
    setLogs(accessLogs || { items: [], total: 0 })
    setDrawer(current => current ? { ...current, application: detail, form: { ...current.form, name: detail.name, description: detail.description || '', enabled: detail.enabled, permission_codes: detail.permission_codes || [] } } : current)
    await load(true)
  }

  const saveApplication = async (event) => {
    event.preventDefault()
    setSaving(true)
    try {
      const payload = { ...drawer.form, expires_at: toRFC3339(drawer.form.expires_at) }
      if (drawer.mode === 'create') {
        const result = await externalAccessAPI.create(payload)
        setDrawer(null)
        setToken(result.token)
        setToast({ type: 'success', message: '外部访问已创建' })
      } else {
        await externalAccessAPI.update(drawer.application.id, payload)
        setDrawer(null)
        setToast({ type: 'success', message: '授权已更新' })
      }
      await load(true)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  const issueCredential = async (form) => {
    setSaving(true)
    try {
      const result = await externalAccessAPI.createCredential(drawer.application.id, form)
      setCredentialDialog(false)
      setToken(result.token)
      await refreshOpenApplication(drawer.application.id)
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  const askDelete = (credential) => setConfirm({
    title: '永久删除 API 凭证',
    message: `确定永久删除「${credential.name}」吗？删除后该 Token 会立即失效，凭证记录无法恢复。`,
    confirmLabel: '永久删除',
    onConfirm: async () => {
      setSaving(true)
      try {
        await externalAccessAPI.deleteCredential(drawer.application.id, credential.id)
        await refreshOpenApplication(drawer.application.id)
        setToast({ type: 'success', message: '凭证已永久删除' })
      } catch (error) {
        setToast({ type: 'error', message: error.message })
      } finally {
        setSaving(false)
        setConfirm(null)
      }
    },
  })

  if (loading) return <div className="dashboard-panel loading-panel"><span className="spinner" /> 加载外部访问...</div>

  return (
    <div className="external-access-page">
      <div className="page-header">
        <div><h1>外部访问</h1><p className="page-subtitle">管理调用方、功能授权、API 凭证和访问记录。</p></div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" disabled={refreshing} onClick={() => load(true)}><RefreshCw size={16} className={refreshing ? 'spin' : ''} /> 刷新</button>
          <button className="btn btn-primary" type="button" onClick={openCreate}><Plus size={16} /> 新增外部访问</button>
        </div>
      </div>

      <div className="summary-grid">
        <div className="summary-tile" data-tone="brand"><span className="summary-icon"><ShieldCheck size={18} /></span><div><div className="summary-value">{summary.total}</div><div className="summary-label">外部应用</div></div></div>
        <div className="summary-tile" data-tone="success"><span className="summary-icon"><Activity size={18} /></span><div><div className="summary-value">{summary.enabled}</div><div className="summary-label">已启用</div></div></div>
        <div className="summary-tile" data-tone="info"><span className="summary-icon"><KeyRound size={18} /></span><div><div className="summary-value">{summary.credentials}</div><div className="summary-label">有效凭证</div></div></div>
        <div className="summary-tile" data-tone="warning"><span className="summary-icon"><Clock3 size={18} /></span><div><div className="summary-value">{summary.used}</div><div className="summary-label">已有调用</div></div></div>
      </div>

      <section className="section data-section">
        <div className="panel-header"><div><h3>调用方列表</h3><div className="panel-caption">权限变更、应用停用和凭证撤销即时生效。</div></div></div>
        <div className="table-wrap">
          <table className="data-table external-access-table">
            <thead><tr><th>外部访问</th><th>状态</th><th>可调用功能</th><th>有效凭证</th><th>最近调用</th><th>操作</th></tr></thead>
            <tbody>
              {applications.map(application => (
                <tr key={application.id}>
                  <td><div className="entity-cell"><span className="entity-icon"><ShieldCheck size={16} /></span><div><strong>{application.name}</strong><span>{application.description || `#${application.id}`}</span></div></div></td>
                  <td><span className={`status-badge ${application.enabled ? 'status-active' : 'status-down'}`}>{application.enabled ? '已启用' : '已停用'}</span></td>
                  <td><div className="tag-list">{(application.permission_codes || []).map(code => <span className="tag tag-info" key={code}>{permissions.find(item => item.code === code)?.name || code}</span>)}</div></td>
                  <td>{(application.credentials || []).filter(item => item.enabled).length}</td>
                  <td><span className="muted-text">{formatDate(application.last_used_at)}</span></td>
                  <td><div className="row-actions"><button className="icon-button compact" type="button" title="编辑与查看详情" onClick={() => openEdit(application)}><Pencil size={15} /></button></div></td>
                </tr>
              ))}
              {applications.length === 0 && <tr><td colSpan={6}><div className="empty-state"><ShieldOff size={28} /><strong>暂无外部访问</strong><span>创建调用方并授予所需功能。</span><button className="btn btn-primary" type="button" onClick={openCreate}><Plus size={16} /> 新增外部访问</button></div></td></tr>}
            </tbody>
          </table>
        </div>
      </section>

      {drawer && <ApplicationDrawer state={drawer} permissions={permissions} saving={saving} logs={logs} onChange={setDrawer} onSave={saveApplication} onClose={() => setDrawer(null)} onIssue={() => setCredentialDialog(true)} onDelete={askDelete} />}
      {credentialDialog && <CredentialDialog saving={saving} onSave={issueCredential} onClose={() => setCredentialDialog(false)} />}
      {token && <TokenDialog token={token} onClose={() => setToken('')} onCopied={() => setToast({ type: 'success', message: 'Token 已复制' })} onCopyError={() => setToast({ type: 'error', message: '复制失败，请手动选择 Token' })} />}
      {confirm && <ConfirmDialog {...confirm} saving={saving} onCancel={() => setConfirm(null)} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
