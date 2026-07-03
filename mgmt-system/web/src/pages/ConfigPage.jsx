import { useState, useEffect, useCallback } from 'react'
import { configAPI } from '../api'
import './ConfigPage.css'

/** Toast 通知组件 */
function Toast({ message, type, onClose }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3000)
    return () => clearTimeout(t)
  }, [onClose])

  return <div className={`toast toast-${type}`}>{message}</div>
}

/** 确认对话框 */
function ConfirmDialog({ title, message, onConfirm, onCancel }) {
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal" style={{ width: 400 }} onClick={e => e.stopPropagation()}>
        <h3>{title}</h3>
        <p style={{ color: '#666', fontSize: 14, marginBottom: 16 }}>{message}</p>
        <div className="modal-footer">
          <button className="btn btn-outline" onClick={onCancel}>取消</button>
          <button className="btn btn-danger" onClick={onConfirm}>确认</button>
        </div>
      </div>
    </div>
  )
}

/** 参数编辑 Modal */
function ConfigModal({ group, configs, onSave, onReset, onClose }) {
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const initial = {}
    configs.forEach(c => { initial[c.key] = c.value })
    setValues(initial)
  }, [configs])

  const handleChange = (key, value) => {
    setValues(prev => ({ ...prev, [key]: value }))
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      const updates = {}
      configs.forEach(c => {
        if (values[c.key] !== c.value) {
          updates[c.key] = values[c.key]
        }
      })
      if (Object.keys(updates).length === 0) {
        onClose()
        return
      }
      await onSave(updates)
      onClose()
    } catch (e) {
      // error handled by parent
    } finally {
      setSaving(false)
    }
  }

  const renderInput = (cfg) => {
    const val = values[cfg.key] ?? cfg.value

    switch (cfg.value_type) {
      case 'bool':
        return (
          <label className="toggle" style={{ marginTop: 4 }}>
            <input
              type="checkbox"
              checked={val === 'true' || val === '1'}
              onChange={e => handleChange(cfg.key, e.target.checked ? 'true' : 'false')}
            />
            <span className="toggle-slider" />
          </label>
        )
      case 'int':
        return (
          <input
            type="number"
            value={val}
            onChange={e => handleChange(cfg.key, e.target.value)}
          />
        )
      default:
        return (
          <input
            type="text"
            value={val}
            onChange={e => handleChange(cfg.key, e.target.value)}
          />
        )
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={e => e.stopPropagation()}>
        <h3>{group.label} - 参数配置</h3>

        {configs.map(cfg => (
          <div key={cfg.key} className="form-group">
            <label>
              {cfg.label}
              {!cfg.reloadable && <span className="reload-badge">* 需重启</span>}
            </label>
            {renderInput(cfg)}
            <div className="form-hint">
              {cfg.description}
              <span style={{ marginLeft: 8, color: '#aaa' }}>
                默认: {cfg.default_value}
              </span>
            </div>
          </div>
        ))}

        <div className="modal-footer">
          <button
            className="btn btn-outline"
            onClick={() => onReset(group.category)}
          >
            恢复默认
          </button>
          <button className="btn btn-outline" onClick={onClose}>取消</button>
          <button
            className="btn btn-success"
            onClick={handleSave}
            disabled={saving}
          >
            {saving && <span className="spinner" />}
            保存
          </button>
        </div>
      </div>
    </div>
  )
}

/** 主页面 */
export default function ConfigPage() {
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)
  const [modalGroup, setModalGroup] = useState(null)
  const [toast, setToast] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [savingAll, setSavingAll] = useState(false)

  const loadConfigs = useCallback(async () => {
    try {
      const data = await configAPI.list()
      setGroups(data.groups || [])
    } catch (e) {
      setToast({ type: 'error', message: '加载配置失败: ' + e.message })
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { loadConfigs() }, [loadConfigs])

  const showToast = (type, message) => {
    setToast({ type, message })
  }

  const handleSaveGroup = async (updates) => {
    try {
      await configAPI.batchUpdate(updates)
      showToast('success', `已保存 ${Object.keys(updates).length} 项配置`)
      loadConfigs()
    } catch (e) {
      showToast('error', '保存失败: ' + e.message)
      throw e
    }
  }

  const handleResetGroup = async (category) => {
    setConfirm({
      title: '恢复默认配置',
      message: `确定要将「${getCategoryLabel(category)}」的所有参数恢复为默认值吗？此操作不可撤销。`,
      onConfirm: async () => {
        try {
          const group = groups.find(g => g.category === category)
          if (group) {
            for (const item of group.items) {
              await configAPI.reset(item.key)
            }
          }
          showToast('success', '已恢复默认值')
          loadConfigs()
        } catch (e) {
          showToast('error', '恢复失败: ' + e.message)
        }
        setConfirm(null)
      },
      onCancel: () => setConfirm(null),
    })
  }

  const handleSaveAll = async () => {
    setSavingAll(true)
    try {
      const allUpdates = {}
      groups.forEach(g => {
        g.items.forEach(item => {
          allUpdates[item.key] = item.value
        })
      })
      // No actual changes to save in "save all" — it's a reload trigger
      await configAPI.reload()
      showToast('success', '已通知所有节点重载配置')
    } catch (e) {
      showToast('error', '操作失败: ' + e.message)
    } finally {
      setSavingAll(false)
    }
  }

  if (loading) {
    return (
      <div className="app-layout">
        <Sidebar />
        <div className="main" style={{ textAlign: 'center', paddingTop: 100, color: '#888' }}>
          加载中...
        </div>
      </div>
    )
  }

  return (
    <div className="app-layout">
      <Sidebar />

      <div className="main">
        <div className="page-header">
          <div>
            <h1>⚙ 系统配置</h1>
            <p className="page-subtitle">
              带 <span className="reload-badge">* 需重启</span> 标记的参数修改后需重启对应服务生效
            </p>
          </div>
          <button
            className="btn btn-primary"
            onClick={handleSaveAll}
            disabled={savingAll}
          >
            {savingAll && <span className="spinner" />}
            通知节点重载
          </button>
        </div>

        {/* 统计卡片 */}
        <div className="cards">
          <div className="card">
            <div className="value">{groups.length}</div>
            <div className="label">配置模块</div>
          </div>
          <div className="card">
            <div className="value">
              {groups.reduce((sum, g) => sum + g.items.length, 0)}
            </div>
            <div className="label">可调参数</div>
          </div>
          <div className="card">
            <div className="value">
              {groups.reduce((sum, g) => sum + g.items.filter(i => i.reloadable).length, 0)}
            </div>
            <div className="label">支持热加载</div>
          </div>
          <div className="card">
            <div className="value">
              {groups.reduce((sum, g) => sum + g.items.filter(i => !i.reloadable).length, 0)}
            </div>
            <div className="label">需重启生效</div>
          </div>
        </div>

        {/* 配置模块表格 */}
        <div className="section">
          <table>
            <thead>
              <tr>
                <th style={{ width: 200 }}>模块名称</th>
                <th style={{ width: 80 }}>启用状态</th>
                <th style={{ width: 80 }}>参数数量</th>
                <th style={{ width: 100 }}>热加载项</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {groups.map(group => {
                const reloadableCount = group.items.filter(i => i.reloadable).length
                const totalCount = group.items.length
                return (
                  <tr key={group.category}>
                    <td>
                      <strong>{group.label}</strong>
                      <div style={{ fontSize: 12, color: '#888' }}>{group.category}</div>
                    </td>
                    <td>
                      <span className="tag tag-success">✅ 启用</span>
                    </td>
                    <td>{totalCount} 项</td>
                    <td>
                      <span className={reloadableCount > 0 ? 'tag tag-info' : 'tag tag-warning'}>
                        {reloadableCount}/{totalCount}
                      </span>
                    </td>
                    <td>
                      <button
                        className="btn btn-sm btn-primary"
                        onClick={() => setModalGroup(group)}
                      >
                        参数配置
                      </button>
                    </td>
                  </tr>
                )
              })}
              {groups.length === 0 && (
                <tr>
                  <td colSpan={5} style={{ textAlign: 'center', padding: 40, color: '#888' }}>
                    暂无配置数据，请检查数据库连接
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Modal */}
      {modalGroup && (
        <ConfigModal
          group={modalGroup}
          configs={modalGroup.items}
          onSave={handleSaveGroup}
          onReset={handleResetGroup}
          onClose={() => setModalGroup(null)}
        />
      )}

      {/* Confirm Dialog */}
      {confirm && <ConfirmDialog {...confirm} />}

      {/* Toast */}
      {toast && (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      )}
    </div>
  )
}

/** 侧边栏 — 与 Go 模板一致的导航 */
function Sidebar() {
  const path = window.location.pathname
  const isActive = (href) => path.startsWith(href) ? 'active' : ''

  return (
    <nav className="sidebar">
      <h2>📧 邮箱管理</h2>
      <a href="/admin/">仪表盘</a>
      <a href="/admin/servers">服务器管理</a>
      <a href="/admin/filters">过滤规则</a>
      <a href="/admin/mailboxes">邮箱管理</a>
      <a href="/admin/emails">邮件查询</a>
      <a href="/admin/config" className={isActive('/admin/config') ? 'active' : ''}>⚙ 系统配置</a>
    </nav>
  )
}

function getCategoryLabel(cat) {
  const map = {
    forward: '邮件转发引擎',
    filter: '过滤引擎',
    lifecycle: '生命周期管理',
    healthcheck: '健康检查',
    heartbeat: '心跳上报',
    session: '管理会话',
    database: '数据库连接池',
    maildir: '邮件存储',
    general: '通用参数',
  }
  return map[cat] || cat
}
