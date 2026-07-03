import { useState, useEffect } from 'react'
import { dashboardAPI, serverAPI } from '../api'

export default function DashboardPage() {
  const [stats, setStats] = useState(null)
  const [servers, setServers] = useState([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    Promise.all([
      dashboardAPI.stats().catch(() => null),
      serverAPI.list().catch(() => []),
    ]).then(([s, sv]) => {
      setStats(s || {})
      setServers(Array.isArray(sv) ? sv : [])
    }).finally(() => setLoading(false))
  }, [])

  if (loading) return <div style={{ textAlign: 'center', paddingTop: 80, color: '#888' }}>加载中...</div>

  const { server_count, healthy_count, active_mailboxes, today_created } = stats || {}

  return (
    <div>
      <h1 style={{ marginBottom: 20 }}>仪表盘</h1>

      <div className="cards">
        <div className="card">
          <div className="value">{healthy_count ?? '-'}/{server_count ?? '-'}</div>
          <div className="label">邮箱服务器（健康/总数）</div>
        </div>
        <div className="card">
          <div className="value">{active_mailboxes ?? '-'}</div>
          <div className="label">活跃邮箱数</div>
        </div>
        <div className="card">
          <div className="value">{today_created ?? '-'}</div>
          <div className="label">今日创建</div>
        </div>
        <div className="card">
          <div className="value">{servers.filter(s => s.status === 'down').length}</div>
          <div className="label">异常节点</div>
        </div>
      </div>

      <div className="section">
        <h3>服务器状态</h3>
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>API 地址</th>
              <th>负载</th>
              <th>状态</th>
              <th>最近探测</th>
              <th>失败次数</th>
            </tr>
          </thead>
          <tbody>
            {servers.map(s => (
              <tr key={s.id}>
                <td><strong>{s.name}</strong></td>
                <td><code>{s.api_host}</code></td>
                <td>{s.current_load} / {s.capacity}</td>
                <td>
                  <span className={`status-dot ${s.status}`} />
                  <span className={`tag tag-${s.status === 'healthy' ? 'success' : s.status === 'degraded' ? 'warning' : s.status === 'draining' ? 'info' : 'danger'}`}>
                    {s.status}
                  </span>
                </td>
                <td>{s.last_probe_at ? new Date(s.last_probe_at).toLocaleString('zh-CN', { hour12: false }) : '-'}</td>
                <td>{s.probe_fail_count > 0 ? <span style={{ color: '#c62828', fontWeight: 600 }}>{s.probe_fail_count}</span> : '0'}</td>
              </tr>
            ))}
            {servers.length === 0 && (
              <tr><td colSpan={6} style={{ textAlign: 'center', color: '#888', padding: 20 }}>暂无服务器</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
