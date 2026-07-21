import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Activity, AlertTriangle, CheckCircle2, Clock3, Copy, FileClock, Filter,
  History, Pencil, Plus, RefreshCw, Save, Send, ShieldCheck, Trash2, X,
} from 'lucide-react'
import { filterPolicyAPI } from '../api'
import { formatDateTime } from '../i18n'
import LegacyFiltersPage from './LegacyFiltersPage'

const TABS = ['overview', 'manual', 'ad', 'decisions', 'quarantines', 'legacy']
const ACTIONS = ['allow', 'tag', 'quarantine']
const MODES = ['shadow', 'enforce', 'disabled']
const COMMON_FIELDS = [
  'header_from.address', 'header_from.domain', 'envelope_from.address', 'envelope_from.domain',
  'reply_to.domain', 'subject', 'text', 'headers', 'has_attachment', 'attachment.filename', 'size_bytes',
]
const AD_FIELDS = [
  ...COMMON_FIELDS, 'list_unsubscribe', 'list_id', 'precedence', 'from_reply_to_domain_match',
  'url_count', 'tracking_pixel_count',
]
const OPERATORS = {
  'header_from.address': ['eq'], 'header_from.domain': ['eq', 'suffix'],
  'envelope_from.address': ['eq'], 'envelope_from.domain': ['eq', 'suffix'],
  'reply_to.domain': ['eq', 'suffix'], 'mailbox.address': ['eq'], subject: ['contains', 'regex'],
  text: ['contains', 'regex'], headers: ['exists'], has_attachment: ['eq'],
  'attachment.filename': ['suffix', 'regex'], size_bytes: ['gte', 'lte'], list_unsubscribe: ['eq'],
  list_id: ['exists'], precedence: ['eq'], from_reply_to_domain_match: ['eq'],
  url_count: ['gte'], tracking_pixel_count: ['gte'],
}
const BOOLEAN_FIELDS = new Set(['has_attachment', 'list_unsubscribe', 'from_reply_to_domain_match'])
const NUMBER_FIELDS = new Set(['size_bytes', 'url_count', 'tracking_pixel_count'])

function Toast({ toast, onClose }) {
  useEffect(() => {
    const timer = setTimeout(onClose, 3500)
    return () => clearTimeout(timer)
  }, [onClose])
  return <div className={`toast toast-${toast.type}`}>{toast.message}</div>
}

function SummaryTile({ icon: Icon, label, value, tone = 'brand' }) {
  return (
    <div className="summary-tile" data-tone={tone}>
      <span className="summary-icon"><Icon size={18} /></span>
      <div><div className="summary-value">{value}</div><div className="summary-label">{label}</div></div>
    </div>
  )
}

function StatusTag({ value }) {
  const tone = value === 'published' || value === 'healthy' ? 'tag-success'
    : value === 'draft' || value === 'tag' ? 'tag-warning'
      : value === 'quarantine' || value === 'error' ? 'tag-danger' : 'tag-info'
  return <span className={`tag ${tone}`}>{value || '-'}</span>
}

function RevisionToolbar({ kind, revisions, selected, detail, busy, onSelect, onCreate, onClone, onValidate, onPublish }) {
  const { t } = useTranslation('pages')
  const editable = detail?.status === 'draft'
  return (
    <div className="filter-policy-toolbar">
      <div className="form-group compact-field">
        <label>{t('filterPolicy.revision')}</label>
        <select value={selected || ''} onChange={event => onSelect(Number(event.target.value))}>
          <option value="">{t('filterPolicy.selectRevision')}</option>
          {revisions.map(item => <option value={item.revision} key={item.revision}>#{item.revision} · {item.status}</option>)}
        </select>
      </div>
      {detail && <div className="revision-facts"><StatusTag value={detail.status} /><code>{detail.checksum || t('filterPolicy.notPublished')}</code></div>}
      <div className="page-actions">
        <button className="btn btn-outline" type="button" onClick={onCreate} disabled={busy}><Plus size={16} />{t('filterPolicy.createDraft')}</button>
        <button className="icon-button" type="button" title={t('filterPolicy.clone')} onClick={onClone} disabled={!detail || busy}><Copy size={16} /></button>
        <button className="btn btn-outline" type="button" onClick={onValidate} disabled={!editable || busy}><CheckCircle2 size={16} />{t('filterPolicy.validate')}</button>
        <button className="btn btn-primary" type="button" onClick={onPublish} disabled={!editable || busy}><Send size={16} />{t('filterPolicy.publish')}</button>
      </div>
      <span className="sr-only">{kind}</span>
    </div>
  )
}

function revisionChanges(kind, detail, base) {
  const items = value => kind === 'manual'
    ? (value?.rules || []).map(item => [`rule:${item.logical_id}`, item])
    : [
        ...(value?.detectors || []).map(item => [`detector:${item.logical_id}`, item]),
        ...(value?.composites || []).map(item => [`composite:${item.logical_id}`, item]),
        ...(value?.weights || []).map(item => [`weight:${item.symbol}`, item]),
      ]
  const current = new Map(items(detail))
  const previous = new Map(items(base))
  let added = 0
  let modified = 0
  let removed = 0
  for (const [key, value] of current) {
    if (!previous.has(key)) added += 1
    else if (JSON.stringify(previous.get(key)) !== JSON.stringify(value)) modified += 1
  }
  for (const key of previous.keys()) if (!current.has(key)) removed += 1
  return { added, modified, removed }
}

function RevisionInsights({ kind, detail, base, validation }) {
  const { t } = useTranslation('pages')
  if (!detail) return null
  const changes = revisionChanges(kind, detail, base)
  const enforceCount = value => kind === 'manual'
    ? (value?.rules || []).filter(item => item.mode === 'enforce').length
    : [...(value?.detectors || []), ...(value?.composites || [])].filter(item => item.mode === 'enforce').length
  const preview = kind === 'manual'
    ? t('filterPolicy.enforcePreview', { before: enforceCount(base), after: enforceCount(detail) })
    : t('filterPolicy.adActionPreview', {
        before: enforceCount(base), after: enforceCount(detail),
        oldTag: base?.tag_threshold ?? '-', tag: detail.tag_threshold,
        oldQuarantine: base?.quarantine_threshold ?? '-', quarantine: detail.quarantine_threshold,
      })
  return (
    <section className="revision-insights" aria-label={t('filterPolicy.revisionDiff')}>
      <div className="revision-delta"><strong>{t('filterPolicy.revisionDiff')}</strong><span>{t('filterPolicy.comparedWith', { revision: detail.base_revision || '-' })}</span></div>
      <div className="revision-metrics"><span data-tone="success">+{changes.added} {t('filterPolicy.added')}</span><span data-tone="warning">{changes.modified} {t('filterPolicy.modified')}</span><span data-tone="danger">-{changes.removed} {t('filterPolicy.removed')}</span></div>
      <div className="action-preview"><strong>{t('filterPolicy.actionPreview')}</strong><code>{preview}</code></div>
      <div className={`validation-result ${validation?.valid ? 'valid' : validation ? 'invalid' : 'pending'}`}>
        <strong>{t('filterPolicy.validationResult')}</strong>
        <span>{validation?.valid ? t('filterPolicy.validBundle', { bytes: validation.bundle_bytes }) : validation?.error || t('filterPolicy.notValidated')}</span>
      </div>
    </section>
  )
}

function defaultCondition(policyKind) {
  const field = policyKind === 'manual' ? 'header_from.domain' : 'subject'
  return { field, operator: OPERATORS[field][0], value: '', negated: false, position: 0 }
}

function normalizedCondition(condition, position) {
  let value = condition.value
  if (condition.field === 'list_id') value = null
  else if (BOOLEAN_FIELDS.has(condition.field)) value = value === true || value === 'true'
  else if (NUMBER_FIELDS.has(condition.field)) value = Number.parseInt(value, 10) || 0
  return { ...condition, value, position }
}

function ConditionEditor({ policyKind, conditions, onChange }) {
  const { t } = useTranslation('pages')
  const fields = policyKind === 'manual' ? [...COMMON_FIELDS, 'mailbox.address'] : AD_FIELDS
  const update = (index, patch) => {
    const next = conditions.map((condition, current) => current === index ? { ...condition, ...patch } : condition)
    onChange(next)
  }
  const remove = index => onChange(conditions.filter((_, current) => current !== index))
  return (
    <div className="condition-editor">
      <div className="condition-editor-header"><strong>{t('filterPolicy.conditions')}</strong><button className="btn btn-outline btn-sm" type="button" onClick={() => onChange([...conditions, defaultCondition(policyKind)])}><Plus size={14} />{t('filterPolicy.addCondition')}</button></div>
      {conditions.map((condition, index) => {
        const operators = OPERATORS[condition.field] || ['eq']
        return (
          <div className="condition-row" key={`${index}-${condition.field}`}>
            <select value={condition.field} onChange={event => update(index, { field: event.target.value, operator: OPERATORS[event.target.value][0], value: event.target.value === 'list_id' ? null : '' })}>
              {fields.map(field => <option key={field} value={field}>{field}</option>)}
            </select>
            <select value={condition.operator} onChange={event => update(index, { operator: event.target.value })}>{operators.map(operator => <option value={operator} key={operator}>{operator}</option>)}</select>
            {condition.field === 'list_id' ? <input value="null" disabled />
              : BOOLEAN_FIELDS.has(condition.field) ? <select value={String(condition.value)} onChange={event => update(index, { value: event.target.value === 'true' })}><option value="true">true</option><option value="false">false</option></select>
                : <input type={NUMBER_FIELDS.has(condition.field) ? 'number' : 'text'} value={condition.value ?? ''} onChange={event => update(index, { value: event.target.value })} required />}
            <label className="condition-negated"><input type="checkbox" checked={condition.negated} onChange={event => update(index, { negated: event.target.checked })} />{t('filterPolicy.negated')}</label>
            <button className="icon-button compact danger" type="button" title={t('common:actions.delete')} onClick={() => remove(index)} disabled={conditions.length === 1}><Trash2 size={14} /></button>
          </div>
        )
      })}
    </div>
  )
}

function PolicyDrawer({ editor, saving, onChange, onSave, onClose }) {
  const { t } = useTranslation('pages')
  const data = editor.data
  const set = patch => onChange({ ...editor, data: { ...data, ...patch } })
  const isComposite = editor.type === 'composite'
  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className="drawer drawer-wide" onClick={event => event.stopPropagation()} aria-label={t('filterPolicy.editor')}>
        <div className="drawer-header"><div><div className="drawer-kicker">{editor.type}</div><h2>{editor.mode === 'edit' ? t('filterPolicy.editItem') : t('filterPolicy.addItem')}</h2></div><button className="icon-button" type="button" onClick={onClose} title={t('common:actions.close')}><X size={18} /></button></div>
        <form className="drawer-body" onSubmit={onSave}>
          <div className="field-grid">
            <div className="form-group"><label>{t('filterPolicy.logicalId')}</label><input value={data.logical_id} onChange={event => set({ logical_id: event.target.value })} required /></div>
            <div className="form-group"><label>{t('filterPolicy.name')}</label><input value={data.name} onChange={event => set({ name: event.target.value })} required /></div>
          </div>
          {editor.type === 'manual' && <div className="field-grid field-grid-3">
            <div className="form-group"><label>{t('filterPolicy.action')}</label><select value={data.action} onChange={event => set({ action: event.target.value })}>{ACTIONS.map(value => <option key={value}>{value}</option>)}</select></div>
            <div className="form-group"><label>{t('filterPolicy.mode')}</label><select value={data.mode} onChange={event => set({ mode: event.target.value })}>{MODES.map(value => <option key={value}>{value}</option>)}</select></div>
            <div className="form-group"><label>{t('filterPolicy.priority')}</label><input type="number" min="0" value={data.priority} onChange={event => set({ priority: Number(event.target.value) })} /></div>
          </div>}
          {editor.type === 'detector' && <div className="field-grid">
            <div className="form-group"><label>{t('filterPolicy.symbol')}</label><input value={data.symbol} onChange={event => set({ symbol: event.target.value.toUpperCase() })} required /></div>
            <div className="form-group"><label>{t('filterPolicy.mode')}</label><select value={data.mode} onChange={event => set({ mode: event.target.value })}>{MODES.map(value => <option key={value}>{value}</option>)}</select></div>
          </div>}
          {isComposite && <>
            <div className="field-grid field-grid-3">
              <div className="form-group"><label>{t('filterPolicy.symbol')}</label><input value={data.symbol} onChange={event => set({ symbol: event.target.value.toUpperCase() })} required /></div>
              <div className="form-group"><label>{t('filterPolicy.mode')}</label><select value={data.mode} onChange={event => set({ mode: event.target.value })}>{MODES.map(value => <option key={value}>{value}</option>)}</select></div>
              <div className="form-group"><label>{t('filterPolicy.scorePolicy')}</label><select value={data.score_policy} onChange={event => set({ score_policy: event.target.value })}><option value="keep_inputs">keep_inputs</option><option value="suppress_direct_inputs">suppress_direct_inputs</option></select></div>
            </div>
            {['all_of', 'any_of', 'none_of'].map(field => <div className="form-group" key={field}><label>{field}</label><input value={(data[field] || []).join(', ')} onChange={event => set({ [field]: event.target.value.split(',').map(value => value.trim()).filter(Boolean) })} /></div>)}
          </>}
          {!isComposite && <ConditionEditor policyKind={editor.type === 'manual' ? 'manual' : 'ad'} conditions={data.conditions} onChange={conditions => set({ conditions })} />}
          <div className="drawer-footer"><button className="btn btn-outline" type="button" onClick={onClose}>{t('common:actions.cancel')}</button><button className="btn btn-primary" type="submit" disabled={saving}><Save size={16} />{t('common:actions.save')}</button></div>
        </form>
      </aside>
    </div>
  )
}

function OverviewPanel({ status, manualRevisions, adRevisions }) {
  const { t } = useTranslation('pages')
  const active = Object.fromEntries((status.active_states || []).map(item => [item.policy_kind, item]))
  const nodes = status.node_states || []
  return <>
    <div className="summary-grid">
      <SummaryTile icon={ShieldCheck} label={t('filterPolicy.activeManual')} value={active.manual?.active_revision || 0} tone="success" />
      <SummaryTile icon={Filter} label={t('filterPolicy.activeAd')} value={active.ad?.active_revision || 0} tone="brand" />
      <SummaryTile icon={History} label={t('filterPolicy.manualVersions')} value={manualRevisions.length} tone="warning" />
      <SummaryTile icon={FileClock} label={t('filterPolicy.adVersions')} value={adRevisions.length} tone="danger" />
    </div>
    <section className="section data-section"><div className="panel-header"><div><h3>{t('filterPolicy.nodeConvergence')}</h3></div></div><div className="table-wrap"><table className="data-table policy-state-table"><thead><tr><th>{t('filterPolicy.node')}</th><th>{t('filterPolicy.kind')}</th><th>desired / applied</th><th>checksum</th><th>{t('filterPolicy.lastError')}</th><th>{t('filterPolicy.updated')}</th></tr></thead><tbody>{nodes.map(item => <tr key={`${item.node_id}-${item.policy_kind}`}><td>#{item.node_id}</td><td>{item.policy_kind}</td><td><strong>{item.desired_revision} / {item.applied_revision}</strong></td><td><code>{item.checksum || '-'}</code></td><td>{item.last_error || '-'}</td><td>{formatDateTime(item.updated_at)}</td></tr>)}{nodes.length === 0 && <tr><td colSpan="6"><div className="empty-state"><Activity size={26} /><strong>{t('filterPolicy.noNodeState')}</strong></div></td></tr>}</tbody></table></div></section>
  </>
}

function ManualPanel({ revisions, selected, detail, base, validation, busy, onSelect, onAction, onEdit }) {
  const { t } = useTranslation('pages')
  const rules = detail?.rules || []
  const editable = detail?.status === 'draft'
  return <>
    <RevisionToolbar kind="manual" revisions={revisions} selected={selected} detail={detail} busy={busy} onSelect={onSelect} onCreate={() => onAction('create')} onClone={() => onAction('clone')} onValidate={() => onAction('validate')} onPublish={() => onAction('publish')} />
    <RevisionInsights kind="manual" detail={detail} base={base} validation={validation} />
    <section className="section data-section"><div className="panel-header"><div><h3>{t('filterPolicy.manualRules')}</h3><div className="panel-caption">{t('filterPolicy.manualCaption')}</div></div>{editable && <button className="btn btn-primary" type="button" onClick={() => onEdit('manual')}><Plus size={16} />{t('filterPolicy.addRule')}</button>}</div><div className="table-wrap"><table className="data-table filters-table"><thead><tr><th>{t('filterPolicy.priority')}</th><th>{t('filterPolicy.name')}</th><th>{t('filterPolicy.action')}</th><th>{t('filterPolicy.mode')}</th><th>{t('filterPolicy.conditions')}</th><th>{t('filterPolicy.operations')}</th></tr></thead><tbody>{rules.map(rule => <tr key={rule.logical_id}><td>{rule.priority}</td><td><strong>{rule.name}</strong><small>{rule.logical_id}</small></td><td><StatusTag value={rule.action} /></td><td><StatusTag value={rule.mode} /></td><td><code>{rule.conditions.map(condition => `${condition.field} ${condition.operator}`).join(' AND ')}</code></td><td><div className="row-actions"><button className="icon-button compact" type="button" disabled={!editable} onClick={() => onEdit('manual', rule)}><Pencil size={14} /></button><button className="icon-button compact danger" type="button" disabled={!editable} onClick={() => onAction('delete-rule', rule)}><Trash2 size={14} /></button></div></td></tr>)}{rules.length === 0 && <tr><td colSpan="6"><div className="empty-state"><AlertTriangle size={26} /><strong>{t('filterPolicy.noRules')}</strong></div></td></tr>}</tbody></table></div></section>
  </>
}

function AdPanel({ revisions, selected, detail, base, validation, busy, weightDraft, onWeightDraft, onSelect, onAction, onEdit }) {
  const { t } = useTranslation('pages')
  const editable = detail?.status === 'draft'
  return <>
    <RevisionToolbar kind="ad" revisions={revisions} selected={selected} detail={detail} busy={busy} onSelect={onSelect} onCreate={() => onAction('create-seed')} onClone={() => onAction('clone')} onValidate={() => onAction('validate')} onPublish={() => onAction('publish')} />
    <RevisionInsights kind="ad" detail={detail} base={base} validation={validation} />
    {detail && <section className="filter-threshold-band"><div className="form-group"><label>{t('filterPolicy.tagThreshold')}</label><input type="number" step="0.001" value={detail.tag_threshold} disabled={!editable} onChange={event => onAction('threshold-local', { field: 'tag_threshold', value: event.target.value })} /></div><div className="form-group"><label>{t('filterPolicy.quarantineThreshold')}</label><input type="number" step="0.001" value={detail.quarantine_threshold} disabled={!editable} onChange={event => onAction('threshold-local', { field: 'quarantine_threshold', value: event.target.value })} /></div><button className="btn btn-outline" type="button" disabled={!editable || busy} onClick={() => onAction('save-thresholds')}><Save size={15} />{t('common:actions.save')}</button></section>}
    <div className="filter-policy-columns">
      <section className="section data-section"><div className="panel-header"><h3>{t('filterPolicy.detectors')}</h3>{editable && <button className="icon-button" type="button" onClick={() => onEdit('detector')}><Plus size={16} /></button>}</div><div className="policy-item-list">{(detail?.detectors || []).map(item => <div className="policy-item" key={item.logical_id}><div><strong>{item.name}</strong><code>{item.symbol}</code></div><StatusTag value={item.mode} /><div className="row-actions"><button className="icon-button compact" disabled={!editable} onClick={() => onEdit('detector', item)}><Pencil size={14} /></button><button className="icon-button compact danger" disabled={!editable} onClick={() => onAction('delete-detector', item)}><Trash2 size={14} /></button></div></div>)}</div></section>
      <section className="section data-section"><div className="panel-header"><h3>{t('filterPolicy.composites')}</h3>{editable && <button className="icon-button" type="button" onClick={() => onEdit('composite')}><Plus size={16} /></button>}</div><div className="policy-item-list">{(detail?.composites || []).map(item => <div className="policy-item" key={item.logical_id}><div><strong>{item.name}</strong><code>{item.symbol}</code></div><StatusTag value={item.mode} /><div className="row-actions"><button className="icon-button compact" disabled={!editable} onClick={() => onEdit('composite', item)}><Pencil size={14} /></button><button className="icon-button compact danger" disabled={!editable} onClick={() => onAction('delete-composite', item)}><Trash2 size={14} /></button></div></div>)}</div></section>
    </div>
    <section className="section data-section"><div className="panel-header"><h3>{t('filterPolicy.weights')}</h3>{editable && <div className="weight-editor"><input placeholder="AD_SYMBOL" value={weightDraft.symbol} onChange={event => onWeightDraft({ ...weightDraft, symbol: event.target.value.toUpperCase() })} /><input type="number" step="0.001" value={weightDraft.score} onChange={event => onWeightDraft({ ...weightDraft, score: event.target.value })} /><button className="btn btn-outline btn-sm" type="button" onClick={() => onAction('save-weight', weightDraft)}><Save size={14} />{t('common:actions.save')}</button></div>}</div><div className="table-wrap"><table className="data-table"><thead><tr><th>{t('filterPolicy.symbol')}</th><th>{t('filterPolicy.score')}</th><th>{t('filterPolicy.operations')}</th></tr></thead><tbody>{(detail?.weights || []).map(item => <tr key={item.symbol}><td><code>{item.symbol}</code></td><td>{item.score}</td><td><button className="icon-button compact danger" disabled={!editable} onClick={() => onAction('delete-weight', item)}><Trash2 size={14} /></button></td></tr>)}</tbody></table></div></section>
  </>
}

function DecisionsPanel({ page, action, selected, onAction, onSelect }) {
  const { t } = useTranslation('pages')
  const items = page.items || []
  return <>
    <div className="filter-decision-toolbar"><select value={action} onChange={event => onAction(event.target.value)}><option value="">{t('filterPolicy.allActions')}</option>{ACTIONS.map(value => <option key={value}>{value}</option>)}</select><span>{t('filterPolicy.totalDecisions', { count: page.total || 0 })}</span></div>
    <section className="section data-section"><div className="table-wrap"><table className="data-table filter-decisions-table"><thead><tr><th>{t('filterPolicy.evaluated')}</th><th>{t('filterPolicy.mailbox')}</th><th>{t('filterPolicy.node')}</th><th>revision</th><th>{t('filterPolicy.score')}</th><th>{t('filterPolicy.action')}</th></tr></thead><tbody>{items.map(item => <tr key={item.decision_key} className="clickable-row" onClick={() => onSelect(item.decision_key)}><td>{formatDateTime(item.evaluated_at)}</td><td>{item.mailbox || `#${item.mailbox_account_id}`}</td><td>#{item.node_id}</td><td>{item.manual_revision} / {item.ad_revision}</td><td>{item.ad_score}</td><td><StatusTag value={item.final_action} /></td></tr>)}{items.length === 0 && <tr><td colSpan="6"><div className="empty-state"><Clock3 size={26} /><strong>{t('filterPolicy.noDecisions')}</strong></div></td></tr>}</tbody></table></div></section>
    {selected && <div className="drawer-overlay" onClick={() => onSelect(null)}><aside className="drawer drawer-wide" onClick={event => event.stopPropagation()}><div className="drawer-header"><div><div className="drawer-kicker">decision</div><h2>{selected.decision_key}</h2></div><button className="icon-button" onClick={() => onSelect(null)}><X size={18} /></button></div><div className="drawer-body decision-detail"><div className="revision-facts"><StatusTag value={selected.final_action} /><strong>{selected.ad_score}</strong><span>{selected.mailbox}</span></div><h3>{t('filterPolicy.reasons')}</h3><pre>{JSON.stringify(selected.reasons, null, 2)}</pre><h3>{t('filterPolicy.symbols')}</h3><pre>{JSON.stringify(selected.ad_symbols, null, 2)}</pre><h3>{t('filterPolicy.shadowResults')}</h3><pre>{JSON.stringify(selected.shadow_results, null, 2)}</pre><h3>{t('filterPolicy.parseWarnings')}</h3><pre>{JSON.stringify(selected.parse_warnings, null, 2)}</pre></div></aside></div>}
  </>
}

const QUARANTINE_STATUSES = ['quarantined', 'releasing', 'release_failed', 'released', 'confirmed_ad', 'expired']

function QuarantinesPanel({ page, status, selected, message, busy, onStatus, onSelect, onAction }) {
  const { t } = useTranslation('pages')
  const [allowScope, setAllowScope] = useState('email')
  const items = page.items || []
  const parsed = message?.message
  return <>
    <div className="filter-decision-toolbar">
      <select value={status} onChange={event => onStatus(event.target.value)}>
        <option value="">{t('filterPolicy.allQuarantineStatuses')}</option>
        {QUARANTINE_STATUSES.map(value => <option value={value} key={value}>{t(`filterPolicy.quarantineStatus.${value}`)}</option>)}
      </select>
      <span>{t('filterPolicy.totalQuarantines', { count: page.total || 0 })}</span>
    </div>
    <section className="section data-section"><div className="table-wrap"><table className="data-table filter-decisions-table"><thead><tr>
      <th>{t('filterPolicy.evaluated')}</th><th>{t('filterPolicy.mailbox')}</th><th>{t('filterPolicy.score')}</th><th>{t('filterPolicy.quarantineState')}</th><th>{t('filterPolicy.expires')}</th>
    </tr></thead><tbody>
      {items.map(item => <tr key={item.quarantine_key} className="clickable-row" onClick={() => onSelect(item.quarantine_key)}><td>{formatDateTime(item.evaluated_at)}</td><td>{item.mailbox}</td><td>{item.ad_score_milli / 1000}</td><td><StatusTag value={item.status} /></td><td>{formatDateTime(item.expires_at)}</td></tr>)}
      {items.length === 0 && <tr><td colSpan="5"><div className="empty-state"><ShieldCheck size={26} /><strong>{t('filterPolicy.noQuarantines')}</strong></div></td></tr>}
    </tbody></table></div></section>
    {selected && <div className="drawer-overlay" onClick={() => onSelect(null)}><aside className="drawer drawer-wide quarantine-review-drawer" onClick={event => event.stopPropagation()}>
      <div className="drawer-header"><div><div className="drawer-kicker">quarantine</div><h2>{parsed?.subject || selected.quarantine_key}</h2></div><button className="icon-button" type="button" title={t('common:actions.close')} onClick={() => onSelect(null)}><X size={18} /></button></div>
      <div className="drawer-body decision-detail quarantine-review-detail">
        <div className="revision-facts"><StatusTag value={selected.status} /><span>{selected.mailbox}</span><strong>{selected.ad_score_milli / 1000}</strong></div>
        {parsed ? <>
          <dl className="quarantine-message-facts"><div><dt>{t('filterPolicy.sender')}</dt><dd>{parsed.from || '-'}</dd></div><div><dt>{t('filterPolicy.messageId')}</dt><dd>{parsed.message_id || selected.message_id || '-'}</dd></div></dl>
          <h3>{t('filterPolicy.messageBody')}</h3><pre>{parsed.text || parsed.body || t('filterPolicy.noMessageBody')}</pre>
          <h3>{t('filterPolicy.attachments')}</h3>
          <div className="quarantine-attachments">{(parsed.attachments || []).map(attachment => <a className="btn btn-outline" key={attachment.index} href={filterPolicyAPI.quarantineAttachmentURL(selected.quarantine_key, attachment.index)}><FileClock size={15} />{attachment.filename || `#${attachment.index}`}</a>)}{!(parsed.attachments || []).length && <span>{t('filterPolicy.noAttachments')}</span>}</div>
        </> : message === false ? <div className="empty-state"><FileClock size={24} /><strong>{t('filterPolicy.quarantineOriginalUnavailable')}</strong></div> : <div className="loading-panel"><span className="spinner" />{t('filterPolicy.loadingQuarantine')}</div>}
        {selected.last_error && <div className="inline-alert error"><AlertTriangle size={16} /><span>{selected.last_error}</span></div>}
      </div>
      <div className="drawer-footer">
        <button className="btn btn-outline" type="button" disabled={busy || selected.status !== 'quarantined'} onClick={() => onAction('confirm', selected)}><CheckCircle2 size={16} />{t('filterPolicy.confirmAd')}</button>
        <select className="quarantine-allow-scope" aria-label={t('filterPolicy.allowScope')} value={allowScope} onChange={event => setAllowScope(event.target.value)}><option value="email">{t('filterPolicy.allowEmail')}</option><option value="domain">{t('filterPolicy.allowDomain')}</option></select>
        <button className="btn btn-outline" type="button" disabled={busy || !['quarantined', 'release_failed'].includes(selected.status)} onClick={() => onAction('allow-release', selected, allowScope)}><Plus size={16} />{t('filterPolicy.allowAndRelease')}</button>
        <button className="btn btn-primary" type="button" disabled={busy || !['quarantined', 'release_failed'].includes(selected.status)} onClick={() => onAction('release', selected)}><Send size={16} />{t('filterPolicy.falsePositiveRelease')}</button>
      </div>
    </aside></div>}
  </>
}

export default function FiltersPage() {
  const { t } = useTranslation('pages')
  const [tab, setTab] = useState('overview')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [toast, setToast] = useState(null)
  const [status, setStatus] = useState({ active_states: [], node_states: [] })
  const [manualRevisions, setManualRevisions] = useState([])
  const [adRevisions, setAdRevisions] = useState([])
  const [manualID, setManualID] = useState(0)
  const [adID, setAdID] = useState(0)
  const [manual, setManual] = useState(null)
  const [ad, setAd] = useState(null)
  const [manualBase, setManualBase] = useState(null)
  const [adBase, setAdBase] = useState(null)
  const [manualValidation, setManualValidation] = useState(null)
  const [adValidation, setAdValidation] = useState(null)
  const [decisions, setDecisions] = useState({ items: [], total: 0 })
  const [decisionAction, setDecisionAction] = useState('')
  const [selectedDecision, setSelectedDecision] = useState(null)
  const [quarantines, setQuarantines] = useState({ items: [], total: 0 })
  const [quarantineStatus, setQuarantineStatus] = useState('')
  const [selectedQuarantine, setSelectedQuarantine] = useState(null)
  const [quarantineMessage, setQuarantineMessage] = useState(null)
  const [editor, setEditor] = useState(null)
  const [weightDraft, setWeightDraft] = useState({ symbol: '', score: '0' })

  const latest = values => values.find(value => value.status === 'draft')?.revision || values[0]?.revision || 0
  const loadBase = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const [nextStatus, nextManual, nextAd, nextDecisions, nextQuarantines] = await Promise.all([
        filterPolicyAPI.status(), filterPolicyAPI.manualRevisions(), filterPolicyAPI.adRevisions(), filterPolicyAPI.decisions({ page: 1, size: 50, action: decisionAction }),
        filterPolicyAPI.quarantines({ page: 1, size: 50, status: quarantineStatus }),
      ])
      setStatus(nextStatus || { active_states: [], node_states: [] })
      setManualRevisions(nextManual || [])
      setAdRevisions(nextAd || [])
      setDecisions(nextDecisions || { items: [], total: 0 })
      setQuarantines(nextQuarantines || { items: [], total: 0 })
      setManualID(current => current || latest(nextManual || []))
      setAdID(current => current || latest(nextAd || []))
    } catch (error) {
      setToast({ type: 'error', message: error.message })
    } finally {
      setLoading(false)
    }
  }, [decisionAction, quarantineStatus])

  useEffect(() => { loadBase() }, [loadBase])
  useEffect(() => { setManualValidation(null); if (manualID) filterPolicyAPI.manualRevision(manualID).then(setManual).catch(error => setToast({ type: 'error', message: error.message })); else setManual(null) }, [manualID])
  useEffect(() => { setAdValidation(null); if (adID) filterPolicyAPI.adRevision(adID).then(setAd).catch(error => setToast({ type: 'error', message: error.message })); else setAd(null) }, [adID])
  useEffect(() => {
    let active = true
    if (!manual?.base_revision) { setManualBase(null); return () => { active = false } }
    filterPolicyAPI.manualRevision(manual.base_revision).then(value => { if (active) setManualBase(value) }).catch(error => setToast({ type: 'error', message: error.message }))
    return () => { active = false }
  }, [manual?.base_revision])
  useEffect(() => {
    let active = true
    if (!ad?.base_revision) { setAdBase(null); return () => { active = false } }
    filterPolicyAPI.adRevision(ad.base_revision).then(value => { if (active) setAdBase(value) }).catch(error => setToast({ type: 'error', message: error.message }))
    return () => { active = false }
  }, [ad?.base_revision])

  const run = async (operation, success) => {
    setBusy(true)
    try {
      const value = await operation()
      setToast({ type: 'success', message: success || t('filterPolicy.saved') })
      await loadBase(true)
      return value
    } catch (error) {
      setToast({ type: 'error', message: error.message })
      return null
    } finally {
      setBusy(false)
    }
  }

  const manualAction = async (action, value) => {
    if (action === 'create') {
      const created = await run(() => filterPolicyAPI.createManual(), t('filterPolicy.draftCreated'))
      if (created) setManualID(created.revision)
    } else if (action === 'clone') {
      const created = await run(() => filterPolicyAPI.cloneManual(manualID), t('filterPolicy.draftCreated'))
      if (created) setManualID(created.revision)
    } else if (action === 'validate') {
      const result = await run(() => filterPolicyAPI.validateManual(manualID), t('filterPolicy.validationPassed'))
      setManualValidation(result)
      if (result && !result.valid) setToast({ type: 'error', message: result.error })
    } else if (action === 'publish' && window.confirm(t('filterPolicy.publishConfirm'))) {
      const published = await run(() => filterPolicyAPI.publishManual(manualID), t('filterPolicy.published'))
      if (published) setManual(published)
    } else if (action === 'delete-rule') {
      const next = manual.rules.filter(rule => rule.logical_id !== value.logical_id)
      const updated = await run(() => filterPolicyAPI.replaceManual(manualID, next))
      if (updated) { setManual(updated); setManualValidation(null) }
    }
  }

  const adAction = async (action, value) => {
    if (action === 'create-seed') {
      const created = await run(() => filterPolicyAPI.createAd({ seed: 'ad-seed-v1' }), t('filterPolicy.draftCreated'))
      if (created) setAdID(created.revision)
    } else if (action === 'clone') {
      const created = await run(() => filterPolicyAPI.cloneAd(adID), t('filterPolicy.draftCreated'))
      if (created) setAdID(created.revision)
    } else if (action === 'validate') {
      const result = await run(() => filterPolicyAPI.validateAd(adID), t('filterPolicy.validationPassed'))
      setAdValidation(result)
      if (result && !result.valid) setToast({ type: 'error', message: result.error })
    } else if (action === 'publish' && window.confirm(t('filterPolicy.publishConfirm'))) {
      const published = await run(() => filterPolicyAPI.publishAd(adID), t('filterPolicy.published'))
      if (published) setAd(published)
    } else if (action === 'threshold-local') { setAd(current => ({ ...current, [value.field]: value.value })); setAdValidation(null) }
    else if (action === 'save-thresholds') {
      const updated = await run(() => filterPolicyAPI.updateAdThresholds(adID, Number(ad.tag_threshold), Number(ad.quarantine_threshold)))
      if (updated) { setAd(updated); setAdValidation(null) }
    } else if (action === 'delete-detector') {
      const updated = await run(() => filterPolicyAPI.removeAdDetector(adID, value.logical_id))
      if (updated) { setAd(updated); setAdValidation(null) }
    } else if (action === 'delete-composite') {
      const updated = await run(() => filterPolicyAPI.removeAdComposite(adID, value.logical_id))
      if (updated) { setAd(updated); setAdValidation(null) }
    } else if (action === 'save-weight' && value.symbol) {
      const updated = await run(() => filterPolicyAPI.putAdWeight(adID, value.symbol, Number(value.score)))
      if (updated) { setAd(updated); setAdValidation(null); setWeightDraft({ symbol: '', score: '0' }) }
    } else if (action === 'delete-weight') {
      const updated = await run(() => filterPolicyAPI.removeAdWeight(adID, value.symbol))
      if (updated) { setAd(updated); setAdValidation(null) }
    }
  }

  const openEditor = (type, value = null) => {
    const defaults = type === 'manual'
      ? { logical_id: `rule-${Date.now()}`, name: '', scope_type: 'global', scope_id: null, action: 'tag', priority: 100, mode: 'shadow', source: 'manual', conditions: [defaultCondition('manual')] }
      : type === 'detector'
        ? { logical_id: `detector-${Date.now()}`, name: '', symbol: 'AD_', mode: 'shadow', source: 'local', source_reference: '', conditions: [defaultCondition('ad')] }
        : { logical_id: `composite-${Date.now()}`, name: '', symbol: 'AD_', mode: 'shadow', score_policy: 'keep_inputs', all_of: [], any_of: [], none_of: [] }
    setEditor({ type, mode: value ? 'edit' : 'add', originalID: value?.logical_id, data: structuredClone(value || defaults) })
  }

  const saveEditor = async event => {
    event.preventDefault()
    const data = { ...editor.data }
    if (data.conditions) data.conditions = data.conditions.map(normalizedCondition)
    let updated
    if (editor.type === 'manual') {
      const rules = [...(manual.rules || [])]
      const index = rules.findIndex(rule => rule.logical_id === editor.originalID)
      if (index >= 0) rules[index] = data; else rules.push(data)
      updated = await run(() => filterPolicyAPI.replaceManual(manualID, rules))
      if (updated) { setManual(updated); setManualValidation(null) }
    } else if (editor.type === 'detector') {
      updated = await run(() => editor.mode === 'edit' ? filterPolicyAPI.updateAdDetector(adID, editor.originalID, data) : filterPolicyAPI.addAdDetector(adID, data))
      if (updated) { setAd(updated); setAdValidation(null) }
    } else {
      updated = await run(() => editor.mode === 'edit' ? filterPolicyAPI.updateAdComposite(adID, editor.originalID, data) : filterPolicyAPI.addAdComposite(adID, data))
      if (updated) { setAd(updated); setAdValidation(null) }
    }
    if (updated) setEditor(null)
  }

  const selectDecision = async key => {
    if (!key) return setSelectedDecision(null)
    try { setSelectedDecision(await filterPolicyAPI.decision(key)) } catch (error) { setToast({ type: 'error', message: error.message }) }
  }

  const selectQuarantine = async key => {
    if (!key) { setSelectedQuarantine(null); setQuarantineMessage(null); return }
    setQuarantineMessage(null)
    try {
      const record = await filterPolicyAPI.quarantine(key)
      setSelectedQuarantine(record)
      if (['released', 'expired'].includes(record.status)) {
        setQuarantineMessage(false)
      } else {
        try { setQuarantineMessage(await filterPolicyAPI.quarantineMessage(key)) }
        catch (error) { setQuarantineMessage(false); setToast({ type: 'error', message: error.message }) }
      }
    } catch (error) { setToast({ type: 'error', message: error.message }) }
  }

  const quarantineAction = async (action, value, allowScope = 'email') => {
    if (action === 'confirm' && window.confirm(t('filterPolicy.confirmAdPrompt'))) {
      const updated = await run(() => filterPolicyAPI.confirmQuarantineAd(value.quarantine_key), t('filterPolicy.feedbackSaved'))
      if (updated) setSelectedQuarantine(updated)
    }
    if (action === 'release' && window.confirm(t('filterPolicy.releasePrompt'))) {
      const updated = await run(() => filterPolicyAPI.releaseQuarantine(value.quarantine_key, 'false_positive'), t('filterPolicy.released'))
      if (updated) setSelectedQuarantine(updated)
    }
    if (action === 'allow-release' && window.confirm(t('filterPolicy.allowReleasePrompt'))) {
      const result = await run(() => filterPolicyAPI.allowAndReleaseQuarantine(value.quarantine_key, allowScope), t('filterPolicy.allowDraftCreated'))
      if (result?.release) setSelectedQuarantine(result.release)
      if (result?.release_error) setToast({ type: 'error', message: `${t('filterPolicy.allowDraftCreated')}: ${result.release_error}` })
    }
  }

  const activeCount = useMemo(() => (status.active_states || []).length, [status])
  if (loading) return <div className="dashboard-panel loading-panel"><span className="spinner" />{t('filterPolicy.loading')}</div>
  return <div>
    <div className="page-header"><div><h1>{t('filterPolicy.title')}</h1><p className="page-subtitle">{t('filterPolicy.subtitle')}</p></div><div className="page-actions"><span className="tag tag-info">{t('filterPolicy.activeKinds', { count: activeCount })}</span><button className="icon-button" type="button" title={t('common:actions.refresh')} onClick={() => loadBase(true)}><RefreshCw size={17} /></button></div></div>
    <div className="phase-tabs policy-tabs filter-main-tabs">{TABS.map(value => <button className={tab === value ? 'active' : ''} key={value} type="button" onClick={() => setTab(value)}>{t(`filterPolicy.tabs.${value}`)}</button>)}</div>
    {tab === 'overview' && <OverviewPanel status={status} manualRevisions={manualRevisions} adRevisions={adRevisions} />}
    {tab === 'manual' && <ManualPanel revisions={manualRevisions} selected={manualID} detail={manual} base={manualBase} validation={manualValidation} busy={busy} onSelect={setManualID} onAction={manualAction} onEdit={openEditor} />}
    {tab === 'ad' && <AdPanel revisions={adRevisions} selected={adID} detail={ad} base={adBase} validation={adValidation} busy={busy} weightDraft={weightDraft} onWeightDraft={setWeightDraft} onSelect={setAdID} onAction={adAction} onEdit={openEditor} />}
    {tab === 'decisions' && <DecisionsPanel page={decisions} action={decisionAction} selected={selectedDecision} onAction={setDecisionAction} onSelect={selectDecision} />}
    {tab === 'quarantines' && <QuarantinesPanel page={quarantines} status={quarantineStatus} selected={selectedQuarantine} message={quarantineMessage} busy={busy} onStatus={setQuarantineStatus} onSelect={selectQuarantine} onAction={quarantineAction} />}
    {tab === 'legacy' && <div className="legacy-filter-panel"><LegacyFiltersPage /></div>}
    {editor && <PolicyDrawer editor={editor} saving={busy} onChange={setEditor} onSave={saveEditor} onClose={() => setEditor(null)} />}
    {toast && <Toast toast={toast} onClose={() => setToast(null)} />}
  </div>
}
