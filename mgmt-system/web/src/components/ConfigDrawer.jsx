import { X } from 'lucide-react'

export default function ConfigDrawer({ title, kicker, icon: Icon, ariaLabel, onClose, children, wide = true }) {
  return (
    <div className="drawer-overlay" onClick={onClose}>
      <aside className={`drawer${wide ? ' drawer-wide' : ''}`} onClick={event => event.stopPropagation()} aria-label={ariaLabel || title}>
        <div className="drawer-header">
          <div className="drawer-title-with-icon">
            {Icon && <span className="module-icon"><Icon size={20} /></span>}
            <div>
              {kicker && <div className="drawer-kicker">{kicker}</div>}
              <h2>{title}</h2>
            </div>
          </div>
          <button className="icon-button" type="button" title="关闭" onClick={onClose}><X size={18} /></button>
        </div>
        {children}
      </aside>
    </div>
  )
}
