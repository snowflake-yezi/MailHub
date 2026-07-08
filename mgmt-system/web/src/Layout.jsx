import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Filter,
  Inbox,
  LayoutDashboard,
  Mail,
  Menu,
  Moon,
  Palette,
  RefreshCw,
  RotateCcw,
  Search,
  Server,
  Settings,
  Sun,
  X,
} from 'lucide-react'

const SIDEBAR_KEY = 'mailhub.sidebar.collapsed'
const THEME_KEY = 'mailhub.theme'
const BRAND_COLOR_KEY = 'mailhub.brandColor'
const DEFAULT_BRAND_COLOR = '#2388ff'
const LOGO_SRC = `${import.meta.env.BASE_URL}mailhub.png`

const BRAND_PRESETS = [
  { label: 'MailHub Blue', value: DEFAULT_BRAND_COLOR },
  { label: 'Mint', value: '#10b981' },
  { label: 'Cyan', value: '#06b6d4' },
  { label: 'Coral', value: '#f97362' },
]

const NAV_ITEMS = [
  { path: '/', label: '仪表盘', icon: LayoutDashboard, group: '总览' },
  { path: '/servers', label: '服务器池', icon: Server, group: '资源' },
  { path: '/mailboxes', label: '邮箱账户', icon: Inbox, group: '资源' },
  { path: '/emails', label: '邮件查询', icon: Mail, group: '资源' },
  { path: '/filters', label: '过滤规则', icon: Filter, group: '策略' },
  { path: '/config', label: '系统配置', icon: Settings, group: '系统' },
]

function getInitialCollapsed() {
  if (typeof window === 'undefined') return false
  return window.localStorage.getItem(SIDEBAR_KEY) === 'true'
}

function getInitialTheme() {
  if (typeof window === 'undefined') return 'light'
  const stored = window.localStorage.getItem(THEME_KEY)
  if (stored === 'light' || stored === 'dark') return stored
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function normalizeHexColor(value) {
  const text = String(value || '').trim()
  const withHash = text.startsWith('#') ? text : `#${text}`
  return /^#[0-9a-fA-F]{6}$/.test(withHash) ? withHash.toLowerCase() : ''
}

function getInitialBrandColor() {
  if (typeof window === 'undefined') return DEFAULT_BRAND_COLOR
  return normalizeHexColor(window.localStorage.getItem(BRAND_COLOR_KEY)) || DEFAULT_BRAND_COLOR
}

function hexToRgb(hex) {
  const value = normalizeHexColor(hex).slice(1)
  return {
    r: parseInt(value.slice(0, 2), 16),
    g: parseInt(value.slice(2, 4), 16),
    b: parseInt(value.slice(4, 6), 16),
  }
}

function toHexChannel(value) {
  return Math.round(value).toString(16).padStart(2, '0')
}

function mixHex(hex, targetHex, targetWeight) {
  const source = hexToRgb(hex)
  const target = hexToRgb(targetHex)
  const sourceWeight = 1 - targetWeight
  return `#${toHexChannel(source.r * sourceWeight + target.r * targetWeight)}${toHexChannel(source.g * sourceWeight + target.g * targetWeight)}${toHexChannel(source.b * sourceWeight + target.b * targetWeight)}`
}

function applyBrandColor(color, theme) {
  const root = document.documentElement
  if (color === DEFAULT_BRAND_COLOR) {
    root.style.removeProperty('--color-brand')
    root.style.removeProperty('--color-brand-strong')
    root.style.removeProperty('--color-brand-soft')
    root.style.removeProperty('--color-bg-accent')
    return
  }

  const rgb = hexToRgb(color)
  root.style.setProperty('--color-brand', color)
  root.style.setProperty('--color-brand-strong', theme === 'dark' ? mixHex(color, '#ffffff', 0.34) : mixHex(color, '#000000', 0.18))
  root.style.setProperty('--color-brand-soft', theme === 'dark' ? `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0.18)` : mixHex(color, '#ffffff', 0.88))
  root.style.setProperty('--color-bg-accent', `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0.12)`)
}

function normalizeGlobalSearch(value) {
  return String(value || '').trim()
}

function isLikelyServerQuery(value) {
  const text = value.toLowerCase()
  return (
    /^(server|node|mail-node|节点|服务器)\s*[:：]/i.test(value) ||
    /^(\d{1,3}\.){3}\d{1,3}(:\d+)?$/.test(value) ||
    text.includes('mail-node') ||
    text.includes('server-')
  )
}

function stripSearchPrefix(value) {
  return value.replace(/^(server|node|mail-node|节点|服务器)\s*[:：]\s*/i, '').trim()
}

export default function Layout({ children }) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const [collapsed, setCollapsed] = useState(getInitialCollapsed)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [theme, setTheme] = useState(getInitialTheme)
  const [brandColor, setBrandColor] = useState(getInitialBrandColor)
  const [customColor, setCustomColor] = useState(getInitialBrandColor)
  const [themePanelOpen, setThemePanelOpen] = useState(false)
  const [globalSearch, setGlobalSearch] = useState('')

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem(THEME_KEY, theme)
    applyBrandColor(brandColor, theme)
  }, [theme, brandColor])

  useEffect(() => {
    if (brandColor === DEFAULT_BRAND_COLOR) window.localStorage.removeItem(BRAND_COLOR_KEY)
    else window.localStorage.setItem(BRAND_COLOR_KEY, brandColor)
    setCustomColor(brandColor)
  }, [brandColor])

  useEffect(() => {
    setMobileOpen(false)
  }, [pathname])

  useEffect(() => {
    if (!mobileOpen) return
    setThemePanelOpen(false)
  }, [mobileOpen])

  const activeItem = useMemo(() => {
    if (pathname === '/search') return { label: '搜索结果' }
    return NAV_ITEMS.find(item => item.path === pathname) || NAV_ITEMS[0]
  }, [pathname])

  const submitGlobalSearch = (e) => {
    e.preventDefault()
    const query = normalizeGlobalSearch(globalSearch)
    if (!query) return

    const normalizedQuery = isLikelyServerQuery(query) ? stripSearchPrefix(query) : query
    navigate(`/search?q=${encodeURIComponent(normalizedQuery)}`)
  }

  const selectBrandColor = (value) => {
    const normalized = normalizeHexColor(value)
    if (!normalized) return
    setBrandColor(normalized)
  }

  const updateCustomColor = (value) => {
    setCustomColor(value)
    const normalized = normalizeHexColor(value)
    if (normalized) setBrandColor(normalized)
  }

  const resetTheme = () => {
    setTheme('light')
    setBrandColor(DEFAULT_BRAND_COLOR)
  }

  return (
    <div className={`app-shell ${collapsed ? 'is-collapsed' : ''}`}>
      <button
        className="mobile-menu-button"
        type="button"
        aria-label="打开导航"
        onClick={() => setMobileOpen(true)}
      >
        <Menu size={20} />
      </button>

      {mobileOpen && (
        <button
          className="sidebar-backdrop"
          type="button"
          aria-label="关闭导航"
          onClick={() => setMobileOpen(false)}
        />
      )}

      <aside className={`sidebar ${mobileOpen ? 'is-mobile-open' : ''}`}>
        <div className="sidebar-brand">
          <Link className="brand-mark" to="/" aria-label="MailHub 仪表盘">
            <img src={LOGO_SRC} alt="" />
          </Link>
          <div className="brand-copy">
            <div className="brand-name">MailHub</div>
            <div className="brand-subtitle">邮件运维控制台</div>
          </div>
          <button
            className="icon-button sidebar-mobile-close"
            type="button"
            aria-label="关闭导航"
            onClick={() => setMobileOpen(false)}
          >
            <X size={18} />
          </button>
        </div>

        <nav className="sidebar-nav" aria-label="主导航">
          {NAV_ITEMS.map((item, index) => {
            const Icon = item.icon
            const previous = NAV_ITEMS[index - 1]
            const showGroup = !previous || previous.group !== item.group
            return (
              <div key={item.path}>
                {showGroup && <div className="nav-group-label">{item.group}</div>}
                <Link
                  to={item.path}
                  title={collapsed ? item.label : undefined}
                  className={`nav-item ${pathname === item.path ? 'active' : ''}`}
                >
                  <Icon className="nav-icon" size={19} />
                  <span className="nav-label">{item.label}</span>
                </Link>
              </div>
            )
          })}
          <div>
            <button
              className={`nav-item theme-nav-item ${themePanelOpen ? 'active' : ''}`}
              type="button"
              title={collapsed ? '主题' : undefined}
              onClick={() => { setMobileOpen(false); setThemePanelOpen(true) }}
            >
              <Palette className="nav-icon" size={19} />
              <span className="nav-label">主题</span>
            </button>
          </div>
        </nav>

        <button
          className="sidebar-collapse"
          type="button"
          aria-label={collapsed ? '展开侧栏' : '收起侧栏'}
          onClick={() => setCollapsed(v => !v)}
        >
          {collapsed ? <ChevronRight size={18} /> : <ChevronLeft size={18} />}
          <span>{collapsed ? '展开' : '收起侧栏'}</span>
        </button>
      </aside>

      <div className="app-main">
        <header className="topbar">
          <div className="topbar-title">
            <span className="topbar-kicker">MailHub</span>
            <strong>{activeItem.label}</strong>
          </div>
          <div className="topbar-actions">
            <form className="global-search" onSubmit={submitGlobalSearch}>
              <Search size={16} />
              <input
                value={globalSearch}
                onChange={e => setGlobalSearch(e.target.value)}
                placeholder="搜索邮箱、节点或 Message-ID"
                aria-label="全局搜索"
              />
            </form>
            <button className="icon-button" type="button" title="刷新">
              <RefreshCw size={18} />
            </button>
          </div>
        </header>
        <main className="main-content">
          {children}
        </main>
      </div>

      {themePanelOpen && (
        <div className="drawer-overlay" onClick={() => setThemePanelOpen(false)}>
          <aside className="drawer theme-drawer" onClick={e => e.stopPropagation()} aria-label="主题">
            <div className="drawer-header">
              <div>
                <div className="drawer-kicker">Theme</div>
                <h2>主题</h2>
              </div>
              <button className="icon-button" type="button" title="关闭" onClick={() => setThemePanelOpen(false)}>
                <X size={18} />
              </button>
            </div>
            <div className="drawer-body theme-drawer-body">
              <section className="theme-section">
                <h3>模式</h3>
                <div className="theme-mode-grid">
                  <button className={`theme-option ${theme === 'light' ? 'active' : ''}`} type="button" onClick={() => setTheme('light')}>
                    <Sun size={17} />
                    <span>浅色</span>
                    {theme === 'light' && <Check size={15} />}
                  </button>
                  <button className={`theme-option ${theme === 'dark' ? 'active' : ''}`} type="button" onClick={() => setTheme('dark')}>
                    <Moon size={17} />
                    <span>深色</span>
                    {theme === 'dark' && <Check size={15} />}
                  </button>
                </div>
              </section>

              <section className="theme-section">
                <h3>品牌色</h3>
                <div className="brand-preset-grid">
                  {BRAND_PRESETS.map(preset => (
                    <button
                      className={`brand-preset ${brandColor === preset.value ? 'active' : ''}`}
                      type="button"
                      key={preset.value}
                      onClick={() => selectBrandColor(preset.value)}
                    >
                      <span className="brand-swatch" style={{ backgroundColor: preset.value }} />
                      <span>{preset.label}</span>
                      {brandColor === preset.value && <Check size={15} />}
                    </button>
                  ))}
                </div>
              </section>

              <section className="theme-section">
                <h3>自定义颜色</h3>
                <div className="custom-color-row">
                  <input
                    className="color-picker"
                    type="color"
                    value={brandColor}
                    aria-label="自定义颜色"
                    onChange={e => updateCustomColor(e.target.value)}
                  />
                  <input
                    value={customColor}
                    onChange={e => updateCustomColor(e.target.value)}
                    placeholder="#2388ff"
                    aria-label="HEX 颜色"
                  />
                </div>
              </section>
            </div>
            <div className="drawer-footer">
              <button className="btn btn-outline" type="button" onClick={resetTheme}>
                <RotateCcw size={15} /> 恢复默认
              </button>
              <button className="btn btn-primary" type="button" onClick={() => setThemePanelOpen(false)}>
                完成
              </button>
            </div>
          </aside>
        </div>
      )}
    </div>
  )
}
