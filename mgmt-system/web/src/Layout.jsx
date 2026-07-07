import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
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
  X,
} from 'lucide-react'

const SIDEBAR_KEY = 'mailhub.sidebar.collapsed'
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

export default function Layout({ children }) {
  const { pathname } = useLocation()
  const [collapsed, setCollapsed] = useState(getInitialCollapsed)
  const [mobileOpen, setMobileOpen] = useState(false)

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    setMobileOpen(false)
  }, [pathname])

  const activeItem = useMemo(() => {
    return NAV_ITEMS.find(item => item.path === pathname) || NAV_ITEMS[0]
  }, [pathname])

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
            <label className="global-search">
              <Search size={16} />
              <input placeholder="搜索邮箱、节点或 Message-ID" />
            </label>
            <button className="icon-button" type="button" title="刷新">
              <RefreshCw size={18} />
            </button>
            <button className="icon-button" type="button" title="主题">
              <Moon size={18} />
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
