import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import i18n from '../src/i18n/index.js'
import { DEFAULT_LANGUAGE, normalizeLanguage } from '../src/i18n/config.js'
import enUS from '../src/i18n/locales/en-US.js'
import jaJP from '../src/i18n/locales/ja-JP.js'
import zhCN from '../src/i18n/locales/zh-CN.js'

function flatten(value, prefix = '', result = new Map()) {
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (child && typeof child === 'object' && !Array.isArray(child)) flatten(child, path, result)
    else result.set(path, String(child))
  }
  return result
}

function interpolationKeys(value) {
  return [...value.matchAll(/{{\s*([^},\s]+)[^}]*}}/g)].map(match => match[1]).sort()
}

const locales = { 'zh-CN': zhCN, 'en-US': enUS, 'ja-JP': jaJP }
const baseline = flatten(zhCN)

for (const [language, resources] of Object.entries(locales)) {
  const entries = flatten(resources)
  assert.deepEqual([...entries.keys()].sort(), [...baseline.keys()].sort(), `${language} resource keys differ from zh-CN`)
  for (const [key, value] of entries) {
    assert.deepEqual(interpolationKeys(value), interpolationKeys(baseline.get(key)), `${language}:${key} interpolation variables differ`)
  }
}

assert.equal(normalizeLanguage('en-US'), 'en-US')
assert.equal(normalizeLanguage('ja-JP'), 'ja-JP')
assert.equal(normalizeLanguage('fr-FR'), DEFAULT_LANGUAGE)
assert.equal(normalizeLanguage(null), DEFAULT_LANGUAGE)

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const sourceDirectory = path.resolve(scriptDirectory, '../src')

async function sourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    if (entry.name === 'locales') continue
    const target = path.join(directory, entry.name)
    if (entry.isDirectory()) files.push(...await sourceFiles(target))
    else if (/\.(js|jsx)$/.test(entry.name)) files.push(target)
  }
  return files
}

for (const file of await sourceFiles(sourceDirectory)) {
  const source = await readFile(file, 'utf8')
  const filename = path.basename(file)
  const defaultNamespace = filename === 'Layout.jsx'
    ? 'layout'
    : filename === 'api.js' || filename === 'ConfigDrawer.jsx' || filename === 'ConfigField.jsx'
      ? 'common'
      : 'pages'
  for (const match of source.matchAll(/\bt\(\s*(['"])([^'"]+)\1/g)) {
    const rawKey = match[2]
    const separator = rawKey.indexOf(':')
    const namespace = separator >= 0 ? rawKey.slice(0, separator) : defaultNamespace
    const key = separator >= 0 ? rawKey.slice(separator + 1) : rawKey
    const candidates = separator >= 0
      ? [`${namespace}.${key}`]
      : [`${namespace}.${key}`, `common.${key}`, `layout.${key}`, `pages.${key}`]
    assert(candidates.some(candidate => baseline.has(candidate)), `${path.relative(sourceDirectory, file)} uses missing key ${namespace}:${key}`)
  }
}

await i18n.changeLanguage('en-US')
assert.equal(i18n.t('layout:nav.dashboard'), 'Dashboard')
await i18n.changeLanguage('ja-JP')
assert.equal(i18n.t('layout:nav.dashboard'), 'ダッシュボード')
await i18n.changeLanguage('fr-FR')
assert.equal(i18n.t('layout:nav.dashboard'), '仪表盘')

console.log(`i18n check passed: ${baseline.size} keys across ${Object.keys(locales).length} languages`)
