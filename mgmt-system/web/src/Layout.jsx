import { useLocation, Link } from 'react-router-dom'

const NAV_ITEMS = [
  { path: '/',            label: '仪表盘',     icon: '📊' },
  { path: '/servers',     label: '服务器管理', icon: '🖥' },
  { path: '/filters',     label: '过滤规则',   icon: '🔍' },
  { path: '/mailboxes',   label: '邮箱管理',   icon: '📬' },
  { path: '/emails',      label: '邮件查询',   icon: '✉️' },
  { path: '/config',      label: '系统配置',   icon: '⚙' },
]

export default function Layout({ children }) {
  const { pathname } = useLocation()

  return (
    <div className="app-layout">
      <nav className="sidebar">
        <h2>📧 邮箱管理</h2>
        {NAV_ITEMS.map(item => (
          <Link
            key={item.path}
            to={item.path}
            className={pathname === item.path ? 'active' : ''}
          >
            {item.icon} {item.label}
          </Link>
        ))}
      </nav>
      <main className="main">
        {children}
      </main>
    </div>
  )
}
