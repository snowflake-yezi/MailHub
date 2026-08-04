const INLINE_IMAGE_CONTENT_TYPES = new Set([
  'image/avif',
  'image/bmp',
  'image/gif',
  'image/jpeg',
  'image/png',
  'image/webp',
])

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
