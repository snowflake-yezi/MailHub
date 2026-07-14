export default function ConfigField({ item, value, onChange, children, action }) {
  const inputValue = value ?? item.value ?? item.global_value ?? ''
  const inputID = `config-${item.key.replace(/[^a-zA-Z0-9_-]/g, '-')}`
  const effectBadge = item.effect_type === 'new_resources'
    ? <span className="tag tag-success">仅影响新建邮箱</span>
    : item.reloadable
      ? <span className="tag tag-info">热加载</span>
      : <span className="tag tag-warning">需重启</span>

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
          <label htmlFor={inputID}>{item.label}</label>
          <code>{item.key}</code>
        </div>
        <div className="config-field-actions">
          {effectBadge}
          {action}
        </div>
      </div>
      <div>{input}</div>
      <div className="form-hint">
        <span>{item.description || '暂无说明'}</span>
        <span>默认: {item.default_value ?? '-'}{item.unit ? ` ${item.unit}` : ''}</span>
      </div>
      {children}
    </section>
  )
}
