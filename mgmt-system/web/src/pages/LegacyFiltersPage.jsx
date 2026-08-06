import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle,
  CheckCircle2,
  Filter,
  Flag,
  Pencil,
  Plus,
  RefreshCw,
  ShieldOff,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-react'
import { filterAPI } from '../api'

const RULE_TYPES = [
  { value: 'whitelist_sender' },
  { value: 'blacklist_sender' },
  { value: 'keyword' },
  { value: 'regex' },
]

const ACTIONS = [
  { value: 'pass', icon: CheckCircle2, tone: 'success' },
  { value: 'flag', icon: Flag, tone: 'warning' },
  { value: 'block', icon: ShieldOff, tone: 'danger' },
]

const TYPE_TAG = {
  whitelist_sender: 'tag-success',
  blacklist_sender: 'tag-danger',
  keyword: 'tag-info',
  regex: 'tag-warning',
}

const ACTION_TAG = {
  pass: 'tag-success',
  flag: 'tag-warning',
  block: 'tag-danger',
}

function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])

  return <div className={`toast toast-${type}`}>{message}</div>
}

function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }) {
  const { t } = useTranslation('common')
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal confirm-modal" onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" type="button" onClick={onCancel}>{t('actions.cancel')}</button>
          <button className="btn btn-danger" type="button" onClick={onConfirm}>{confirmLabel || t('actions.confirm')}</button>
        </div>
      </div>
    </div>
  )
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

function FilterDrawer({ modal, saving, onChange, onSave, onClose }) {
  const { t } = useTranslation('pages')
  const updateField = (field, value) => {
    onChange({ ...modal, data: { ...modal.data, [field]: value } })
  }

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer" onClick={e => e.stopPropagation()} aria-label={modal.mode === 'add' ? t('filters.drawer.addAria') : t('filters.drawer.editAria')}>
        <div className="drawer-header">
          <div className="drawer-title-with-icon">
            <span className="module-icon"><SlidersHorizontal size={18} /></span>
            <div>
              <div className="drawer-kicker">{t('filters.drawer.kicker')}</div>
              <h2>{modal.mode === 'add' ? t('filters.drawer.addTitle') : t('filters.drawer.editTitle', { name: modal.data.name || `#${modal.data.id}` })}</h2>
            </div>
          </div>
          <button className="icon-button" type="button" title={t('common:actions.close')} onClick={onClose}><X size={18} /></button>
        </div>

        <form className="drawer-body" onSubmit={onSave}>
          <div className="form-group">
            <label>{t('filters.drawer.name')}</label>
            <input value={modal.data.name} onChange={e => updateField('name', e.target.value)} placeholder={t('filters.drawer.namePlaceholder')} required />
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>{t('filters.drawer.type')}</label>
              <select value={modal.data.rule_type} onChange={e => updateField('rule_type', e.target.value)}>
                {RULE_TYPES.map(type => <option key={type.value} value={type.value}>{t(`filters.ruleTypes.${type.value}`)}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label>{t('filters.drawer.action')}</label>
              <select value={modal.data.action} onChange={e => updateField('action', e.target.value)}>
                {ACTIONS.map(action => <option key={action.value} value={action.value}>{t(`filters.actions.${action.value}`)}</option>)}
              </select>
            </div>
          </div>
          <div className="form-group">
            <label>{t('filters.drawer.pattern')}</label>
            <input value={modal.data.pattern} onChange={e => updateField('pattern', e.target.value)} placeholder={t('filters.drawer.patternPlaceholder')} required />
            <div className="form-hint">{t('filters.drawer.patternHint')}</div>
          </div>
          <div className="field-grid">
            <div className="form-group">
              <label>{t('filters.drawer.priority')}</label>
              <input type="number" value={modal.data.priority} onChange={e => updateField('priority', parseInt(e.target.value, 10) || 0)} min={0} />
              <div className="form-hint">{t('filters.drawer.priorityHint')}</div>
            </div>
            <div className="form-group">
              <label>{t('filters.drawer.enabled')}</label>
              <label className="switch-row">
                <span className="toggle">
                  <input type="checkbox" checked={modal.data.enabled} onChange={e => updateField('enabled', e.target.checked)} />
                  <span className="toggle-slider" />
                </span>
                {modal.data.enabled ? t('common:states.enabled') : t('common:states.disabled')}
              </label>
            </div>
          </div>

          <div className="drawer-footer">
            <button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button>
            <button className="btn btn-primary" type="submit" disabled={saving}>
              {saving && <span className="spinner" />} {t('common:actions.save')}
            </button>
          </div>
        </form>
      </aside>
    </div>
  )
}

export default function LegacyFiltersPage() {
  const { t } = useTranslation('pages')
  const [rules, setRules] = useState([])
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [modal, setModal] = useState(null)
  const [saving, setSaving] = useState(false)
  const [actionFilter, setActionFilter] = useState('all')

  const load = useCallback((silent = false) => {
    if (!silent) setLoading(true)
    filterAPI.list()
      .then(data => setRules(Array.isArray(data) ? data : []))
      .catch(e => setToast({ type: 'error', message: t('common:errors.loadFailed', { message: e.message }) }))
      .finally(() => setLoading(false))
  }, [t])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => {
    const enabled = rules.filter(r => r.enabled).length
    const byAction = rules.reduce((acc, rule) => {
      acc[rule.action] = (acc[rule.action] || 0) + 1
      return acc
    }, {})
    return {
      total: rules.length,
      enabled,
      pass: byAction.pass || 0,
      flag: byAction.flag || 0,
      block: byAction.block || 0,
    }
  }, [rules])

  const visibleRules = useMemo(() => {
    const sorted = [...rules].sort((a, b) => (a.priority || 0) - (b.priority || 0))
    return actionFilter === 'all' ? sorted : sorted.filter(r => r.action === actionFilter)
  }, [rules, actionFilter])

  const openAdd = () => setModal({
    mode: 'add',
    data: { name: '', rule_type: 'whitelist_sender', pattern: '', action: 'pass', priority: 10, enabled: true },
  })

  const openEdit = (r) => setModal({ mode: 'edit', data: { ...r } })

  const handleSave = async (e) => {
    e.preventDefault()
    setSaving(true)
    try {
      if (modal.mode === 'add') await filterAPI.create(modal.data)
      else await filterAPI.update(modal.data.id, modal.data)
      setModal(null)
      setToast({ type: 'success', message: t('filters.messages.saved') })
      load(true)
    } catch (err) {
      setToast({ type: 'error', message: err.message })
    } finally {
      setSaving(false)
    }
  }

  const askDelete = (rule) => {
    setConfirm({
      title: t('filters.dialogs.deleteTitle'),
      message: t('filters.dialogs.deleteMessage', { name: rule.name }),
      confirmLabel: t('common:actions.delete'),
      onConfirm: async () => {
        try {
          await filterAPI.remove(rule.id)
          setToast({ type: 'success', message: t('filters.messages.deleted') })
          load(true)
        } catch (err) {
          setToast({ type: 'error', message: err.message })
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const toggleEnabled = async (rule) => {
    try {
      await filterAPI.update(rule.id, { ...rule, enabled: !rule.enabled })
      setToast({ type: 'success', message: rule.enabled ? t('filters.messages.disabled') : t('filters.messages.enabled') })
      load(true)
    } catch (err) {
      setToast({ type: 'error', message: err.message })
    }
  }

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> {t('filters.loading')}
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('filters.title')}</h1>
          <p className="page-subtitle">{t('filters.subtitle')}</p>
        </div>
        <div className="page-actions">
          <button className="btn btn-outline" type="button" onClick={() => load(true)}>
            <RefreshCw size={16} /> {t('common:actions.refresh')}
          </button>
          <button className="btn btn-primary" type="button" onClick={openAdd}>
            <Plus size={16} /> {t('filters.add')}
          </button>
        </div>
      </div>

      <div className="summary-grid">
        <SummaryTile icon={Filter} label={t('filters.summary.total')} value={summary.total} tone="brand" />
        <SummaryTile icon={CheckCircle2} label={t('filters.summary.enabled')} value={summary.enabled} tone="success" />
        <SummaryTile icon={Flag} label={t('filters.summary.flagged')} value={summary.flag} tone="warning" />
        <SummaryTile icon={ShieldOff} label={t('filters.summary.blocked')} value={summary.block} tone="danger" />
      </div>

      <div className="phase-tabs policy-tabs">
        <button className={actionFilter === 'all' ? 'active' : ''} type="button" onClick={() => setActionFilter('all')}>{t('filters.all')}</button>
        {ACTIONS.map(action => (
          <button className={actionFilter === action.value ? 'active' : ''} type="button" key={action.value} onClick={() => setActionFilter(action.value)}>
            {t(`filters.actions.${action.value}`)}
          </button>
        ))}
      </div>

      <section className="section data-section">
        <div className="panel-header">
          <div>
            <h3>{t('filters.list.title')}</h3>
            <div className="panel-caption">{t('filters.list.caption')}</div>
          </div>
        </div>

        <div className="table-wrap">
          <table className="data-table filters-table">
            <thead>
              <tr>
                <th>{t('filters.list.priority')}</th>
                <th>{t('filters.list.rule')}</th>
                <th>{t('filters.list.type')}</th>
                <th>{t('filters.list.pattern')}</th>
                <th>{t('filters.list.action')}</th>
                <th>{t('filters.list.enabled')}</th>
                <th>{t('filters.list.operations')}</th>
              </tr>
            </thead>
            <tbody>
              {visibleRules.map(rule => (
                <tr key={rule.id}>
                  <td><span className="priority-pill">{rule.priority}</span></td>
                  <td>
                    <div className="entity-cell">
                      <span className="entity-icon"><SlidersHorizontal size={16} /></span>
                      <div>
                        <strong>{rule.name}</strong>
                        <span>#{rule.id}</span>
                      </div>
                    </div>
                  </td>
                  <td><span className={`tag ${TYPE_TAG[rule.rule_type] || 'tag-info'}`}>{t(`filters.ruleTypes.${rule.rule_type}`, { defaultValue: rule.rule_type })}</span></td>
                  <td><code>{rule.pattern}</code></td>
                  <td><span className={`tag ${ACTION_TAG[rule.action] || 'tag-info'}`}>{t(`filters.actionLabels.${rule.action}`, { defaultValue: rule.action })}</span></td>
                  <td>
                    <label className="switch-row compact-switch">
                      <span className="toggle">
                        <input type="checkbox" checked={rule.enabled} onChange={() => toggleEnabled(rule)} />
                        <span className="toggle-slider" />
                      </span>
                    </label>
                  </td>
                  <td>
                    <div className="row-actions">
                      <button className="icon-button compact" type="button" title={t('common:actions.edit')} onClick={() => openEdit(rule)}>
                        <Pencil size={15} />
                      </button>
                      <button className="icon-button compact danger" type="button" title={t('common:actions.delete')} onClick={() => askDelete(rule)}>
                        <Trash2 size={15} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {visibleRules.length === 0 && (
                <tr>
                  <td colSpan={7}>
                    <div className="empty-state">
                      <AlertTriangle size={28} />
                      <strong>{t('filters.list.empty')}</strong>
                      <span>{t('filters.list.emptyDesc')}</span>
                      <button className="btn btn-primary" type="button" onClick={openAdd}><Plus size={16} /> {t('filters.add')}</button>
                    </div>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {modal && <FilterDrawer modal={modal} saving={saving} onChange={setModal} onSave={handleSave} onClose={() => setModal(null)} />}
      {confirm && <ConfirmDialog {...confirm} />}
      {toast && <Toast {...toast} onClose={() => setToast(null)} />}
    </div>
  )
}
