import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
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
import { formatDateTime } from '../i18n'

const LOGO_SRC = `${import.meta.env.BASE_URL}mailhub.png`

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
  const { t } = useTranslation('pages')
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
        title: t('dashboard.attention.downTitle', { count: summary.down }),
        desc: t('dashboard.attention.downDesc'),
        color: 'var(--color-danger)',
      })
    }
    if (summary.degraded > 0) {
      items.push({
        icon: AlertTriangle,
        title: t('dashboard.attention.degradedTitle', { count: summary.degraded }),
        desc: t('dashboard.attention.degradedDesc'),
        color: 'var(--color-warning)',
      })
    }
    if (summary.draining > 0) {
      items.push({
        icon: Activity,
        title: t('dashboard.attention.drainingTitle', { count: summary.draining }),
        desc: t('dashboard.attention.drainingDesc'),
        color: 'var(--color-info)',
      })
    }
    if (items.length === 0) {
      items.push({
        icon: CheckCircle2,
        title: t('dashboard.attention.healthyTitle'),
        desc: t('dashboard.attention.healthyDesc'),
        color: 'var(--color-success)',
      })
    }
    return items
  }, [summary, t])

  if (loading) {
    return (
      <div className="dashboard-panel loading-panel">
        <span className="spinner" /> {t('dashboard.loading')}
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
            <h1>{t('dashboard.title')}</h1>
            <p className="hero-subtitle">
              {t('dashboard.subtitle')}
            </p>
          </div>
        </div>
        <div className="hero-actions">
          <Link className="btn btn-primary" to="/mailboxes?view=create">
            <MailPlus size={17} /> {t('dashboard.createMailbox')}
          </Link>
          <Link className="btn btn-outline" to="/emails">
            <Search size={17} /> {t('dashboard.searchEmail')}
          </Link>
          <button className="btn btn-outline" type="button" onClick={() => load(true)} disabled={refreshing}>
            {refreshing ? <span className="spinner" /> : <RefreshCw size={17} />}
            {t('common:actions.refresh')}
          </button>
        </div>
      </section>

      <div className="metric-grid">
        <MetricCard
          icon={Server}
          label={t('dashboard.metrics.servers')}
          value={`${summary.healthy ?? '-'}/${summary.total ?? '-'}`}
          hint={t('dashboard.metrics.serverPool')}
          color="var(--color-brand)"
        />
        <MetricCard
          icon={Inbox}
          label={t('dashboard.metrics.activeMailboxes')}
          value={stats.active_mailboxes ?? '-'}
          hint={t('dashboard.metrics.accounts')}
          color="var(--color-success)"
        />
        <MetricCard
          icon={Sparkles}
          label={t('dashboard.metrics.createdToday')}
          value={stats.today_created ?? '-'}
          hint={t('dashboard.metrics.growth')}
          color="var(--color-info)"
        />
        <MetricCard
          icon={ShieldCheck}
          label={t('dashboard.metrics.unhealthyNodes')}
          value={summary.down + summary.degraded}
          hint={t('dashboard.metrics.pending')}
          color={summary.down + summary.degraded > 0 ? 'var(--color-danger)' : 'var(--color-success)'}
        />
      </div>

      <div className="dashboard-grid">
        <section className="dashboard-panel">
          <div className="panel-header">
            <div>
              <h3>{t('dashboard.load.title')}</h3>
              <div className="panel-caption">{t('dashboard.load.caption')}</div>
            </div>
            <Link className="btn btn-sm btn-outline" to="/servers">
              {t('dashboard.load.viewServers')} <ArrowUpRight size={14} />
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
                      {t(`dashboard.status.${server.status}`, { defaultValue: server.status || t('common:states.unknown') })}
                    </span>
                  </div>
                  <div className="progress" aria-label={t('dashboard.load.aria', { name: server.name, percent })}>
                    <div
                      className="progress-bar"
                      style={{
                        '--progress': `${percent}%`,
                        '--progress-color': loadColor(percent, server.status),
                      }}
                    />
                  </div>
                  <div className="panel-caption">
                    {t('dashboard.load.detail', { current: server.current_load || 0, capacity: server.capacity || 0, date: formatDateTime(server.last_probe_at) })}
                  </div>
                </div>
              )
            })}
            {servers.length === 0 && (
              <div className="attention-item">
                <span className="attention-icon"><Server size={18} /></span>
                <div>
                  <div className="attention-title">{t('dashboard.load.noServers')}</div>
                  <div className="attention-desc">{t('dashboard.load.noServersDesc')}</div>
                </div>
              </div>
            )}
          </div>
        </section>

        <section className="dashboard-panel">
          <div className="panel-header">
            <div>
              <h3>{t('dashboard.attention.title')}</h3>
              <div className="panel-caption">{t('dashboard.attention.caption')}</div>
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
