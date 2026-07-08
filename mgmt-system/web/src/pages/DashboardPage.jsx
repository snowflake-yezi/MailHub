import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  CheckCircle2,
  CircleOff,
  Inbox,
  MailPlus,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  Sparkles,
} from 'lucide-react'
import { dashboardAPI, serverAPI } from '../api'

const STATUS_LABELS = {
  healthy: '健康',
  degraded: '降级',
  draining: '缩容中',
  down: '离线',
}
const LOGO_SRC = `${import.meta.env.BASE_URL}mailhub.png`

function formatDate(value) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function statusClass(status) {
  return `status-badge status-${status || 'down'}`
}

function clampLoad(current, capacity) {
  const total = Number(capacity) || 0
  if (total <= 0) return 0
  return Math.max(0, Math.min(100, Math.round(((Number(current) || 0) / total) * 100)))
}

function loadColor(percent, status) {
  if (status === 'down') return 'var(--color-danger)'
  if (status === 'degraded' || percent >= 80) return 'var(--color-warning)'
  if (status === 'draining') return 'var(--color-info)'
  return 'var(--color-success)'
}

function MetricCard({ icon: Icon, label, value, hint, color }) {
  return (
    <div className="metric-card" style={{ '--metric-color': color }}>
      <div className="metric-topline">
        <span className="metric-icon"><Icon size={19} /></span>
        <span className="panel-caption">{hint}</span>
      </div>
      <div className="metric-value">{value}</div>
      <div className="metric-label">{label}</div>
    </div>
  )
}

function AttentionItem({ icon: Icon, title, desc, color }) {
  return (
    <div className="attention-item">
      <span className="attention-icon" style={{ '--attention-color': color }}>
        <Icon size={18} />
      </span>
      <div>
        <div className="attention-title">{title}</div>
        <div className="attention-desc">{desc}</div>
      </div>
    </div>
  )
}

export default function DashboardPage() {
  const [stats, setStats] = useState({})
  const [servers, setServers] = useState([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    try {
      const [s, sv] = await Promise.all([
        dashboardAPI.stats().catch(() => ({})),
        serverAPI.list().catch(() => []),
      ])
      setStats(s || {})
      setServers(Array.isArray(sv) ? sv : [])
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const summary = useMemo(() => {
    const total = stats.server_count ?? servers.length
    const healthy = stats.healthy_count ?? servers.filter(s => s.status === 'healthy').length
    const down = servers.filter(s => s.status === 'down').length
    const degraded = servers.filter(s => s.status === 'degraded').length
    const draining = servers.filter(s => s.status === 'draining').length
    return { total, healthy, down, degraded, draining }
  }, [stats, servers])

  const attentionItems = useMemo(() => {
    const items = []
    if (summary.down > 0) {
      items.push({
        icon: CircleOff,
        title: `${summary.down} 个节点离线`,
        desc: '优先检查心跳、Nginx 反代和 mail-node systemd 状态',
        color: 'var(--color-danger)',
      })
    }
    if (summary.degraded > 0) {
      items.push({
        icon: AlertTriangle,
        title: `${summary.degraded} 个节点降级`,
        desc: '建议查看主动探测失败次数和最近探测时间',
        color: 'var(--color-warning)',
      })
    }
    if (summary.draining > 0) {
      items.push({
        icon: Activity,
        title: `${summary.draining} 个节点缩容中`,
        desc: '缩容节点不会继续分配新邮箱，可在服务器池恢复',
        color: 'var(--color-info)',
      })
    }
    if (items.length === 0) {
      items.push({
        icon: CheckCircle2,
        title: '系统运行平稳',
        desc: '暂无离线或降级节点，可以继续处理邮箱和规则配置',
        color: 'var(--color-success)',
      })
    }
    return items
  }, [summary])

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> 正在载入 MailHub 控制台...
      </div>
    )
  }

  return (
    <div>
      <section className="dashboard-hero">
        <div className="hero-copy">
          <div className="hero-logo">
            <img src={LOGO_SRC} alt="" />
          </div>
          <div>
            <h1>MailHub 运维总控台</h1>
            <p className="hero-subtitle">
              汇总节点健康、邮箱增长、转发链路和配置状态，先看异常，再做操作。
            </p>
          </div>
        </div>
        <div className="hero-actions">
          <Link className="btn btn-primary" to="/mailboxes">
            <MailPlus size={17} /> 创建邮箱
          </Link>
          <Link className="btn btn-outline" to="/emails">
            <Search size={17} /> 查询邮件
          </Link>
          <button className="btn btn-outline" type="button" onClick={() => load(true)} disabled={refreshing}>
            {refreshing ? <span className="spinner" /> : <RefreshCw size={17} />}
            刷新
          </button>
        </div>
      </section>

      <div className="metric-grid">
        <MetricCard
          icon={Server}
          label="邮箱服务器（健康/总数）"
          value={`${summary.healthy ?? '-'}/${summary.total ?? '-'}`}
          hint="节点池"
          color="var(--color-brand)"
        />
        <MetricCard
          icon={Inbox}
          label="活跃邮箱数"
          value={stats.active_mailboxes ?? '-'}
          hint="账户"
          color="var(--color-success)"
        />
        <MetricCard
          icon={Sparkles}
          label="今日创建"
          value={stats.today_created ?? '-'}
          hint="增长"
          color="var(--color-info)"
        />
        <MetricCard
          icon={ShieldCheck}
          label="异常节点"
          value={summary.down + summary.degraded}
          hint="待处理"
          color={summary.down + summary.degraded > 0 ? 'var(--color-danger)' : 'var(--color-success)'}
        />
      </div>

      <div className="dashboard-grid">
        <section className="dashboard-panel">
          <div className="panel-header">
            <div>
              <h3>服务器负载</h3>
              <div className="panel-caption">按节点展示当前邮箱分配量和最近探测状态</div>
            </div>
            <Link className="btn btn-sm btn-outline" to="/servers">
              查看服务器池 <ArrowUpRight size={14} />
            </Link>
          </div>

          <div className="server-load-list">
            {servers.map(server => {
              const percent = clampLoad(server.current_load, server.capacity)
              return (
                <div className="server-load-item" key={server.id}>
                  <div className="server-load-main">
                    <div>
                      <div className="server-name">{server.name || `server-${server.id}`}</div>
                      <div className="server-api">{server.api_host || '-'}</div>
                    </div>
                    <span className={statusClass(server.status)}>
                      {STATUS_LABELS[server.status] || server.status || '未知'}
                    </span>
                  </div>
                  <div className="progress" aria-label={`${server.name} 负载 ${percent}%`}>
                    <div
                      className="progress-bar"
                      style={{
                        '--progress': `${percent}%`,
                        '--progress-color': loadColor(percent, server.status),
                      }}
                    />
                  </div>
                  <div className="panel-caption">
                    {server.current_load || 0} / {server.capacity || 0} 邮箱 · 最近探测 {formatDate(server.last_probe_at)}
                  </div>
                </div>
              )
            })}
            {servers.length === 0 && (
              <div className="attention-item">
                <span className="attention-icon"><Server size={18} /></span>
                <div>
                  <div className="attention-title">还没有服务器节点</div>
                  <div className="attention-desc">先注册一台 mail-node，MailHub 才能开始分配邮箱。</div>
                </div>
              </div>
            )}
          </div>
        </section>

        <section className="dashboard-panel">
          <div className="panel-header">
            <div>
              <h3>待处理事项</h3>
              <div className="panel-caption">异常优先，正常时展示系统摘要</div>
            </div>
          </div>
          <div className="attention-list">
            {attentionItems.map(item => (
              <AttentionItem key={item.title} {...item} />
            ))}
          </div>
        </section>
      </div>
    </div>
  )
}
