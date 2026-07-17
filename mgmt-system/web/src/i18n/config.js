export const LANGUAGE_STORAGE_KEY = 'mailhub.language'
export const DEFAULT_LANGUAGE = 'zh-CN'

export const SUPPORTED_LANGUAGES = [
  { code: 'zh-CN', label: '简体中文', shortLabel: '中' },
  { code: 'en-US', label: 'English', shortLabel: 'EN' },
  { code: 'ja-JP', label: '日本語', shortLabel: '日' },
]

const SUPPORTED_LANGUAGE_CODES = new Set(SUPPORTED_LANGUAGES.map(language => language.code))

export function normalizeLanguage(value) {
  return SUPPORTED_LANGUAGE_CODES.has(value) ? value : DEFAULT_LANGUAGE
}

export function getStoredLanguage(storage) {
  if (!storage) return DEFAULT_LANGUAGE
  try {
    return normalizeLanguage(storage.getItem(LANGUAGE_STORAGE_KEY))
  } catch {
    return DEFAULT_LANGUAGE
  }
}
