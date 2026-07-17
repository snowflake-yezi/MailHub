import { useTranslation } from 'react-i18next'

const UNIT_KEYS = {
  '字节': 'bytes',
  '天': 'days',
  '小时': 'hours',
  '邮箱': 'mailbox',
  '毫秒': 'milliseconds',
  '分钟': 'minutes',
  '秒': 'seconds',
  '开关': 'switch',
  '版本': 'version',
}

export default function ConfigField({ item, value, onChange, children, action }) {
  const { t } = useTranslation(['common', 'pages'])
  const inputValue = value ?? item.value ?? item.global_value ?? ''
  const inputID = `config-${item.key.replace(/[^a-zA-Z0-9_-]/g, '-')}`
  const itemKey = `pages:config.fields.${item.key}`
  const label = t(`${itemKey}.label`, { defaultValue: item.label })
  const description = t(`${itemKey}.description`, { defaultValue: item.description || t('config.noDescription') })
  const unit = item.unit ? t(`units.${UNIT_KEYS[item.unit]}`, { defaultValue: item.unit }) : ''
  const effectBadge = item.effect_type === 'new_resources'
    ? <span className="tag tag-success">{t('config.effects.newResources')}</span>
    : item.reloadable
      ? <span className="tag tag-info">{t('config.effects.hotReload')}</span>
      : <span className="tag tag-warning">{t('config.effects.restart')}</span>

  const input = item.value_type === 'bool' ? (
    <label className="toggle">
      <input
        id={inputID}
        type="checkbox"
        checked={inputValue === 'true' || inputValue === '1' || inputValue === true}
        onChange={event => onChange(event.target.checked ? 'true' : 'false')}
      />
      <span className="toggle-slider" />
    </label>
  ) : item.value_type === 'int' ? (
    <input
      id={inputID}
      type="number"
      min={item.min}
      max={item.max}
      value={inputValue}
      onChange={event => onChange(event.target.value)}
      required
    />
  ) : String(inputValue).length > 72 ? (
    <textarea id={inputID} rows={3} value={inputValue} onChange={event => onChange(event.target.value)} />
  ) : (
    <input id={inputID} type="text" value={inputValue} onChange={event => onChange(event.target.value)} />
  )

  return (
    <section className="config-field">
      <div className="config-field-head">
        <div>
          <label htmlFor={inputID}>{label}</label>
          <code>{item.key}</code>
        </div>
        <div className="config-field-actions">
          {effectBadge}
          {action}
        </div>
      </div>
      <div>{input}</div>
      <div className="form-hint">
        <span>{description}</span>
        <span>{t('config.defaultValue', { value: `${item.default_value ?? '-'}${unit ? ` ${unit}` : ''}` })}</span>
      </div>
      {children}
    </section>
  )
}
