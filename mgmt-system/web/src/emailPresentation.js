const INLINE_IMAGE_CONTENT_TYPES = new Set([
  'image/avif',
  'image/bmp',
  'image/gif',
  'image/jpeg',
  'image/png',
  'image/webp',
])

const EMAIL_PREVIEW_CSP_PREFIX = "default-src 'none';"
const EMAIL_PREVIEW_CSP_SUFFIX = "style-src 'unsafe-inline'; font-src data:; base-uri 'none'; form-action 'none'"
const SAFE_EMAIL_LINK_PROTOCOLS = new Set(['http:', 'https:', 'mailto:', 'tel:'])

export function buildEmailPreviewCSP(origin) {
  let httpOrigin = ''
  try {
    const parsed = new URL(String(origin || ''))
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
      httpOrigin = parsed.origin
    }
  } catch {
    // Invalid origins are omitted so the preview remains closed by default.
  }

  const imageSources = ["'self'", ...(httpOrigin ? [httpOrigin] : []), 'https:', 'data:', 'blob:']
  return `${EMAIL_PREVIEW_CSP_PREFIX} img-src ${imageSources.join(' ')}; upgrade-insecure-requests; ${EMAIL_PREVIEW_CSP_SUFFIX}`
}

export function normalizeRemoteImageURL(value) {
  const candidate = String(value || '').trim()
  if (!candidate) return ''

  try {
    const parsed = new URL(candidate.startsWith('//') ? `https:${candidate}` : candidate)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return ''
    parsed.protocol = 'https:'
    return parsed.href
  } catch {
    return ''
  }
}

export function normalizeEmailLinkURL(value) {
  const candidate = String(value || '').trim()
  if (!candidate) return ''

  try {
    const parsed = new URL(candidate.startsWith('//') ? `https:${candidate}` : candidate)
    return SAFE_EMAIL_LINK_PROTOCOLS.has(parsed.protocol) ? parsed.href : ''
  } catch {
    return ''
  }
}

export function normalizeContentType(value) {
  return String(value || '').split(';')[0].trim().toLowerCase()
}

export function isInlineBodyImage(attachment) {
  return Boolean(attachment?.inline)
    && INLINE_IMAGE_CONTENT_TYPES.has(normalizeContentType(attachment?.content_type))
}

export function partitionEmailAttachments(detail) {
  const inlineImages = []
  const fileAttachments = []

  for (const attachment of detail?.attachments || []) {
    if (isInlineBodyImage(attachment)) {
      inlineImages.push(attachment)
    } else {
      fileAttachments.push(attachment)
    }
  }

  return { inlineImages, fileAttachments }
}

export function countFileAttachments(message) {
  if (!Array.isArray(message?.attachments)) {
    return Number(message?.attachments_count) || 0
  }
  return partitionEmailAttachments(message).fileAttachments.length
}
