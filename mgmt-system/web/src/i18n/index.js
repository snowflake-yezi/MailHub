import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import { DEFAULT_LANGUAGE, getStoredLanguage, LANGUAGE_STORAGE_KEY, normalizeLanguage } from './config.js'
import enUS from './locales/en-US.js'
import jaJP from './locales/ja-JP.js'
import zhCN from './locales/zh-CN.js'

function getStorage() {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    return null
  }
}

const storage = getStorage()

i18n
  .use(initReactI18next)
  .init({
    resources: {
      'zh-CN': zhCN,
      'en-US': enUS,
      'ja-JP': jaJP,
    },
    lng: getStoredLanguage(storage),
    fallbackLng: DEFAULT_LANGUAGE,
    defaultNS: 'common',
    fallbackNS: 'common',
    supportedLngs: ['zh-CN', 'en-US', 'ja-JP'],
    interpolation: { escapeValue: false },
    initImmediate: false,
  })

function applyLanguage(language) {
  const normalized = normalizeLanguage(language)
  if (typeof document !== 'undefined') document.documentElement.lang = normalized
  if (storage) {
    try {
      storage.setItem(LANGUAGE_STORAGE_KEY, normalized)
    } catch {
      // The selected language still applies when storage is unavailable.
    }
  }
}

applyLanguage(i18n.resolvedLanguage)
i18n.on('languageChanged', applyLanguage)

export function formatDateTime(value, options = {}) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(i18n.resolvedLanguage || DEFAULT_LANGUAGE, {
    dateStyle: 'medium',
    timeStyle: 'medium',
    hour12: false,
    ...options,
  }).format(date)
}

export default i18n
