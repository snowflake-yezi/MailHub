import assert from 'node:assert/strict'
import { buildCallTemplate, buildResourceReference, TEMPLATE_LANGUAGES } from '../src/externalApiReference.js'

const t = (key, options = {}) => options.name ? `${key}:${options.name}` : key
const permission = {
  code: 'email:body',
  name: 'Read email body',
  description: 'Read a parsed email body',
}

const bodyResource = {
  method: 'GET',
  path: '/api/v1/emails/:message_id/body',
  name: 'Read email body',
  permission_code: permission.code,
  permission,
}
const bodyReference = buildResourceReference(bodyResource, t)
assert.deepEqual(bodyReference.parameters.map(item => `${item.location}:${item.name}`), ['path:message_id', 'query:mailbox'])

for (const language of TEMPLATE_LANGUAGES) {
  const template = buildCallTemplate(bodyResource, bodyReference, language, 'https://mail.example.com/')
  assert.match(template, /https:\/\/mail\.example\.com\/api\/v1\/emails\/%3Cmessage-id%40example\.com%3E\/body\?mailbox=order-xxx%40example\.com/)
  assert.match(template, /Bearer <token>/)
}

const mailboxResource = {
  method: 'POST',
  path: '/api/v1/mailboxes',
  name: 'Create mailbox',
  permission_code: 'mailbox:create',
  permission: { ...permission, code: 'mailbox:create' },
}
const mailboxReference = buildResourceReference(mailboxResource, t)
assert.equal(mailboxReference.requestBody.order_id, 'ORDER-20260703-001')
const javascriptMailbox = buildCallTemplate(mailboxResource, mailboxReference, 'javascript', 'https://mail.example.com')
assert.match(javascriptMailbox, /JSON\.stringify/)
assert.doesNotThrow(() => new Function(`return async function callExternalAPI() { ${javascriptMailbox} }`))

const attachmentResource = { ...bodyResource, path: '/api/v1/emails/:message_id/attachments/:index' }
const attachmentReference = buildResourceReference(attachmentResource, t)
assert.equal(attachmentReference.responseKind, 'binary')
assert.match(buildCallTemplate(attachmentResource, attachmentReference, 'curl', 'https://mail.example.com'), /--output 'attachment\.bin'/)

const publishResource = {
  ...bodyResource,
  method: 'POST',
  path: '/api/v1/manual-filter-revisions/:revision/publish',
}
const publishReference = buildResourceReference(publishResource, t)
assert.equal(publishReference.extraHeaders['Idempotency-Key'], 'publish-request-001')
const pythonPublish = buildCallTemplate(publishResource, publishReference, 'python', 'https://mail.example.com')
assert.match(pythonPublish, /"Idempotency-Key": "publish-request-001"/)
assert.match(pythonPublish, /\n\)\nresponse\.raise_for_status\(\)/)

const futureResource = { ...bodyResource, path: '/api/v1/future/:resource_id' }
const futureReference = buildResourceReference(futureResource, t)
assert.equal(futureReference.parameters[0].name, 'resource_id')
assert.match(buildCallTemplate(futureResource, futureReference, 'curl', 'https://mail.example.com'), /future\/example-resource_id/)

console.log('External API reference check passed: details and curl/JavaScript/Python templates are generated')
