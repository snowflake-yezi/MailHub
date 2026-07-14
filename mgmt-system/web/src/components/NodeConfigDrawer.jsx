import { useCallback, useEffect, useMemo, useState } from 'react'
import { RotateCcw, Save, Settings2 } from 'lucide-react'
import { serverAPI } from '../api'
import ConfigDrawer from './ConfigDrawer'
import ConfigField from './ConfigField'

const STATUS_META = {
  unreported: { label: '未上报', detail: '节点尚未上报配置事实' },
  pending_apply: { label: '等待应用', detail: '配置版本已更新，等待节点拉取并确认应用' },
  pending_retry: { label: '等待重试', detail: '即时通知失败，节点将通过定时拉取自动重试' },
  apply_failed: { label: '应用失败', detail: '节点拉取、校验或应用配置失败' },
  pending_restart: { label: '等待重启', detail: '该配置需要节点重启后生效' },
  restart_detected: { label: '已检测重启', detail: '节点已经重启，等待新进程上报匹配配置' },
  restart_overdue: { label: '重启逾期', detail: '配置变更后长时间未检测到节点重启' },
  applied: { label: '已应用', detail: '节点已确认应用当前配置版本' },
}

export function configStatusMeta(status) {
  return STATUS_META[status] || STATUS_META.unreported
}

export function reloadToast(result, savedMessage) {
  if (result?.requires_restart) return { type: 'success', message: `${savedMessage}，需重启节点后生效` }
  if (result?.reload_dispatched) return { type: 'success', message: `${savedMessage}，已通知节点热加载` }
  return { type: 'error', message: `${savedMessage}，但节点通知失败，将由定时拉取重试` }
}

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function sourceLabel(source) {
  return source === 'server_override' ? '节点覆盖' : source === 'global' ? '全局配置' : source === 'local_config' ? '本地配置' : '未知'
}

export default function NodeConfigDrawer({ server, category, onClose, onChanged, onToast }) {
  const [data, setData] = useState(null)
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState(false)

  const loadConfig = useCallback(async () => {
    try {
      const result = await serverAPI.configs(server.id)
      setData(result)
      setValues(Object.fromEntries((result.items || []).map(item => [item.key, item.override_value ?? item.global_value ?? ''])))
    } catch (error) {
      onToast({ type: 'error', message: '节点配置加载失败: ' + error.message })
    }
  }, [onToast, server.id])

  useEffect(() => { loadConfig() }, [loadConfig])

  const items = useMemo(() => (data?.items || []).filter(item => !category || item.category === category), [category, data])
  const dirtyItems = useMemo(() => items.filter(item => {
    const original = item.override_value ?? item.global_value ?? ''
    return String(values[item.key] ?? '') !== String(original)
  }), [items, values])

  const save = async event => {
    event.preventDefault()
    if (dirtyItems.length === 0) return onClose()
    setSaving(true)
    try {
      let result
      for (const item of dirtyItems) result = await serverAPI.updateConfig(server.id, item.key, values[item.key])
      onToast(reloadToast(result, `已保存 ${dirtyItems.length} 项节点覆盖`))
      await loadConfig()
      onChanged?.()
    } catch (error) {
      onToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  const reset = async item => {
    setSaving(true)
    try {
      const result = await serverAPI.resetConfig(server.id, item.key)
      onToast(reloadToast(result, `「${item.label}」已恢复跟随全局`))
      await loadConfig()
      onChanged?.()
    } catch (error) {
      onToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  return (
    <ConfigDrawer title={category ? `${category === 'forward' ? '邮件转发引擎' : '生命周期管理'} · 节点覆盖` : '节点配置'} kicker={server.name || `server-${server.id}`} icon={Settings2} onClose={onClose}>
      {!data ? <div className="drawer-body loading-panel"><span className="spinner" /> 加载配置...</div> : (
        <form className="drawer-body" onSubmit={save}>
          <div className="node-config-identity">
            <strong>{data.server_name}</strong>
            <code>{data.api_host}</code>
            <div className="node-config-runtime">
              <span>版本 {data.applied_revision ?? 0} / {data.desired_revision ?? 0}</span>
              <span>启动 {formatDate(data.last_started_at)}</span>
              <span>Boot ID {data.last_boot_id ? data.last_boot_id.slice(0, 12) : '未上报'}</span>
            </div>
          </div>
          <div className="config-drawer-summary">
            <span className={dirtyItems.length ? 'tag tag-warning' : 'tag tag-success'}>{dirtyItems.length ? `${dirtyItems.length} 项未保存` : '无未保存修改'}</span>
            <span className="tag tag-info">节点覆盖优先于全局默认</span>
          </div>
          <div className="config-field-list">
            {items.map(item => {
              const status = configStatusMeta(item.status)
              return (
                <ConfigField
                  key={item.key}
                  item={item}
                  value={values[item.key]}
                  onChange={value => setValues(previous => ({ ...previous, [item.key]: value }))}
                  action={item.override_value != null && (
                    <button className="icon-button compact" type="button" title="恢复全局默认" disabled={saving} onClick={() => reset(item)}><RotateCcw size={14} /></button>
                  )}
                >
                  <div className="config-facts config-facts-compact">
                    <div><span>全局默认</span><strong>{item.global_value} {item.unit}</strong></div>
                    <div><span>节点覆盖</span><strong>{item.override_value != null ? `${item.override_value} ${item.unit}` : '无'}</strong></div>
                    <div><span>实际生效</span><strong>{item.effective_value != null ? `${item.effective_value} ${item.unit}` : '未上报'}</strong></div>
                    <div><span>配置来源</span><strong>{sourceLabel(item.source)}</strong></div>
                  </div>
                  <div className={`config-apply-status ${item.status}`}>
                    <strong>{status.label}</strong>
                    <span>{status.detail}{item.status === 'applied' ? ` · ${formatDate(item.reported_at)}` : ''}</span>
                    {data.last_apply_error && <code>{data.last_apply_error}</code>}
                    {data.last_reload_error && !data.last_apply_error && <code>{data.last_reload_error}</code>}
                  </div>
                </ConfigField>
              )
            })}
          </div>
          <section className="config-audit-section">
            <div className="section-heading"><div><span className="eyebrow">Audit</span><h3>最近配置变更</h3></div></div>
            {(data.audits || []).length === 0 ? <div className="empty-inline">暂无节点配置变更记录</div> : (
              <div className="config-audit-list">
                {data.audits.map(audit => (
                  <div className="config-audit-item" key={audit.id}>
                    <div><strong>{audit.config_key}</strong><span>{audit.action === 'reset' ? '恢复全局' : '设置覆盖'} · revision {audit.desired_revision}</span></div>
                    <code>{audit.old_value || '无覆盖'} → {audit.new_value}</code>
                    <span>{audit.actor} · {formatDate(audit.created_at)}</span>
                  </div>
                ))}
              </div>
            )}
          </section>
          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>取消</button>
            <button className="btn btn-primary" type="submit" disabled={saving || dirtyItems.length === 0}>
              {saving ? <span className="spinner" /> : <Save size={16} />} 保存覆盖
            </button>
          </div>
        </form>
      )}
    </ConfigDrawer>
  )
}
