import { useCallback, useEffect, useMemo, useState } from 'react'
import { RotateCcw, Save, Settings2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { serverAPI } from '../api'
import { formatDateTime } from '../i18n'
import ConfigDrawer from './ConfigDrawer'
import ConfigField from './ConfigField'

const STATUS_KEYS = new Set(['unreported', 'pending_apply', 'pending_retry', 'apply_failed', 'pending_restart', 'restart_detected', 'restart_overdue', 'applied'])

export function configStatusMeta(status, t) {
  const key = STATUS_KEYS.has(status) ? status : 'unreported'
  return {
    label: t(`config.node.statuses.${key}.label`),
    detail: t(`config.node.statuses.${key}.detail`),
  }
}

export function reloadToast(result, savedMessage, t) {
  if (result?.requires_restart) return { type: 'success', message: t('config.node.toastRestart', { message: savedMessage }) }
  if (result?.reload_dispatched) return { type: 'success', message: t('config.node.toastReload', { message: savedMessage }) }
  return { type: 'error', message: t('config.node.toastRetry', { message: savedMessage }) }
}

function displayConfigValue(item, value, fallback) {
  if (item.key === 'lifecycle.message_retention_days' && String(value) === '0') return fallback
  return value != null ? `${value} ${item.unit}` : fallback
}

export default function NodeConfigDrawer({ server, category, onClose, onChanged, onToast }) {
  const { t } = useTranslation('pages')
  const [data, setData] = useState(null)
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState(false)

  const loadConfig = useCallback(async () => {
    try {
      const result = await serverAPI.configs(server.id)
      setData(result)
      setValues(Object.fromEntries((result.items || []).map(item => [item.key, item.override_value ?? item.global_value ?? ''])))
    } catch (error) {
      onToast({ type: 'error', message: t('config.node.loadFailed', { message: error.message }) })
    }
  }, [onToast, server.id, t])

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
      onToast(reloadToast(result, t('config.node.savedOverrides', { count: dirtyItems.length }), t))
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
      const name = t(`config.fields.${item.key}.label`, { defaultValue: item.label })
      onToast(reloadToast(result, t('config.node.resetDone', { name }), t))
      await loadConfig()
      onChanged?.()
    } catch (error) {
      onToast({ type: 'error', message: error.message })
    } finally {
      setSaving(false)
    }
  }

  return (
    <ConfigDrawer title={category ? t('config.node.categoryTitle', { name: t(`config.categories.${category}.label`) }) : t('config.node.title')} kicker={server.name || `server-${server.id}`} icon={Settings2} onClose={onClose}>
      {!data ? <div className="drawer-body loading-panel"><span className="spinner" /> {t('config.node.loading')}</div> : (
        <form className="drawer-body" onSubmit={save}>
          <div className="node-config-identity">
            <strong>{data.server_name}</strong>
            <code>{data.api_host}</code>
            <div className="node-config-runtime">
              <span>{t('config.node.version', { applied: data.applied_revision ?? 0, desired: data.desired_revision ?? 0 })}</span>
              <span>{t('config.node.started', { date: formatDateTime(data.last_started_at) })}</span>
              <span>{t('config.node.bootId', { value: data.last_boot_id ? data.last_boot_id.slice(0, 12) : t('common:states.notReported') })}</span>
            </div>
          </div>
          <div className="config-drawer-summary">
            <span className={dirtyItems.length ? 'tag tag-warning' : 'tag tag-success'}>{dirtyItems.length ? t('config.node.dirty', { count: dirtyItems.length }) : t('config.node.noChanges')}</span>
            <span className="tag tag-info">{t('config.node.priorityHint')}</span>
          </div>
          <div className="config-field-list">
            {items.map(item => {
              const status = configStatusMeta(item.status, t)
              const isMessageRetention = item.key === 'lifecycle.message_retention_days'
              const effectiveDisplay = isMessageRetention && String(item.effective_value) === '0'
                ? t('config.node.mailboxRetention')
                : displayConfigValue(item, item.effective_value, t('common:states.notReported'))
              return (
                <ConfigField
                  key={item.key}
                  item={item}
                  value={values[item.key]}
                  onChange={value => setValues(previous => ({ ...previous, [item.key]: value }))}
                  action={item.override_value != null && (
                    <button className="icon-button compact" type="button" title={t('config.node.resetGlobal')} disabled={saving} onClick={() => reset(item)}><RotateCcw size={14} /></button>
                  )}
                >
                  <div className="config-facts config-facts-compact">
                    <div><span>{t('config.node.globalDefault')}</span><strong>{displayConfigValue(item, item.global_value, t('config.node.useMailboxSetting'))}</strong></div>
                    <div><span>{t('config.node.nodeOverride')}</span><strong>{item.override_value != null ? `${item.override_value} ${item.unit}` : t('config.node.none')}</strong></div>
                    <div><span>{t('config.node.effective')}</span><strong>{effectiveDisplay}</strong></div>
                    <div><span>{t('config.node.source')}</span><strong>{t(`config.node.sources.${item.source}`, { defaultValue: t('config.node.sources.unknown') })}</strong></div>
                  </div>
                  {item.related_hint && <div className="inline-alert">{item.related_hint}</div>}
                  <div className={`config-apply-status ${item.status}`}>
                    <strong>{status.label}</strong>
                    <span>{isMessageRetention ? t('config.node.lifecycleRuntimeHint') : status.detail}{item.status === 'applied' && item.reported_at ? ` · ${formatDateTime(item.reported_at)}` : ''}</span>
                    {data.last_apply_error && <code>{data.last_apply_error}</code>}
                    {data.last_reload_error && !data.last_apply_error && <code>{data.last_reload_error}</code>}
                  </div>
                </ConfigField>
              )
            })}
          </div>
          <section className="config-audit-section">
            <div className="section-heading"><div><span className="eyebrow">Audit</span><h3>{t('config.node.recentChanges')}</h3></div></div>
            {(data.audits || []).length === 0 ? <div className="empty-inline">{t('config.node.noAudits')}</div> : (
              <div className="config-audit-list">
                {data.audits.map(audit => (
                  <div className="config-audit-item" key={audit.id}>
                    <div><strong>{audit.config_key}</strong><span>{audit.action === 'reset' ? t('config.node.auditReset') : t('config.node.auditOverride')} · revision {audit.desired_revision}</span></div>
                    <code>{audit.old_value || t('config.node.noOverride')} → {audit.new_value}</code>
                    <span>{audit.actor} · {formatDateTime(audit.created_at)}</span>
                  </div>
                ))}
              </div>
            )}
          </section>
          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button>
            <button className="btn btn-primary" type="submit" disabled={saving || dirtyItems.length === 0}>
              {saving ? <span className="spinner" /> : <Save size={16} />} {t('config.node.saveOverrides')}
            </button>
          </div>
        </form>
      )}
    </ConfigDrawer>
  )
}
