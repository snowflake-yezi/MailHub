import assert from 'node:assert/strict'
import { countFileAttachments, isInlineBodyImage, partitionEmailAttachments } from '../src/emailPresentation.js'

const inlineLogo = { index: 2, inline: true, content_type: 'Image/PNG; name="logo.png"' }
const inlineAVIF = { index: 3, inline: true, content_type: 'image/avif' }
const attachedPhoto = { index: 0, inline: false, content_type: 'image/jpeg' }
const inlinePDF = { index: 1, inline: true, content_type: 'application/pdf' }
const inlineSVG = { index: 4, inline: true, content_type: 'image/svg+xml' }

assert.equal(isInlineBodyImage(inlineLogo), true, 'inline raster images belong to the message body')
assert.equal(isInlineBodyImage(inlineAVIF), true, 'safe AVIF images belong to the message body')
assert.equal(isInlineBodyImage(attachedPhoto), false, 'regular image attachments stay in the attachment list')
assert.equal(isInlineBodyImage(inlinePDF), false, 'non-image inline parts stay downloadable')
assert.equal(isInlineBodyImage(inlineSVG), false, 'active image formats must not enter the body renderer')

const partitioned = partitionEmailAttachments({
  attachments: [attachedPhoto, inlinePDF, inlineLogo, inlineAVIF, inlineSVG],
})
assert.deepEqual(partitioned.inlineImages.map(item => item.index), [2, 3])
assert.deepEqual(partitioned.fileAttachments.map(item => item.index), [0, 1, 4])
assert.deepEqual(partitionEmailAttachments(null), { inlineImages: [], fileAttachments: [] })
assert.equal(countFileAttachments({ attachments: [attachedPhoto, inlineLogo] }), 1)
assert.equal(countFileAttachments({ attachments_count: 3 }), 3, 'legacy list responses retain their count fallback')

console.log('Email presentation check passed: inline body images are separated from file attachments')
