import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './Layout'
import DashboardPage from './pages/DashboardPage'
import ServersPage from './pages/ServersPage'
import FiltersPage from './pages/FiltersPage'
import MailboxesPage from './pages/MailboxesPage'
import EmailsPage from './pages/EmailsPage'
import ConfigPage from './pages/ConfigPage'
import SearchPage from './pages/SearchPage'

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/servers" element={<ServersPage />} />
        <Route path="/filters" element={<FiltersPage />} />
        <Route path="/mailboxes" element={<MailboxesPage />} />
        <Route path="/emails" element={<EmailsPage />} />
        <Route path="/config" element={<ConfigPage />} />
        <Route path="/search" element={<SearchPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Layout>
  )
}
