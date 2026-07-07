import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './App.css'

const viteBase = import.meta.env.BASE_URL.replace(/\/$/, '')
const basename = window.location.pathname.startsWith(viteBase)
  ? viteBase
  : window.location.pathname.startsWith('/admin')
    ? '/admin'
    : ''

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter basename={basename}>
      <App />
    </BrowserRouter>
  </React.StrictMode>
)
