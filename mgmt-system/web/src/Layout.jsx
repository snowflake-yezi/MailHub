import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import {
  ChevronLeft,
  ChevronRight,
  Filter,
  Inbox,
  LayoutDashboard,
  Mail,
  Menu,
  Moon,
  RefreshCw,
  Search,
  Server,
  Settings,
  Sun,
  X,
} from 'lucide-react'

const SIDEBAR_KEY = 'mailhub.sidebar.collapsed'
const THEME_KEY = 'mailhub.theme'
const LOGO_SRC = `${import.meta.env.BASE_URL}mailhub.png`

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
  const [globalSearch, setGlobalSearch] = useState('')

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem(THEME_KEY, theme)
  }, [theme])

  useEffect(() => {
    setMobileOpen(false)
  }, [pathname])

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
            <button
              className="icon-button"
              type="button"
              title={theme === 'dark' ? '切换浅色模式' : '切换深色模式'}
              aria-label={theme === 'dark' ? '切换浅色模式' : '切换深色模式'}
              aria-pressed={theme === 'dark'}
              onClick={() => setTheme(v => (v === 'dark' ? 'light' : 'dark'))}
            >
              {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
            </button>
          </div>
        </header>
        <main className="main-content">
          {children}
        </main>
      </div>
    </div>
  )
}
