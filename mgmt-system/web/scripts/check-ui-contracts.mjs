import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const sourceDirectory = path.resolve(scriptDirectory, '../src')

async function source(relativePath) {
  return readFile(path.join(sourceDirectory, relativePath), 'utf8')
}

const [api, servers, externalAccess, mailboxes, emails, filters] = await Promise.all([
  source('api.js'),
  source('pages/ServersPage.jsx'),
  source('pages/ExternalAccessPage.jsx'),
  source('pages/MailboxesPage.jsx'),
  source('pages/EmailsPage.jsx'),
  source('pages/FiltersPage.jsx'),
])

for (const field of ['smtp_host', 'imap_host']) {
  assert.match(servers, new RegExp(`value=\\{form\\.${field}\\}`), `server editor is missing ${field}`)
  assert.match(servers, new RegExp(`${field}: form\\.${field}`), `server update payload is missing ${field}`)
}

assert.match(api, /revokeCredential\(id, credentialId\).*\/revoke.*method: 'POST'/, 'credential revoke API mapping is missing')
assert.match(api, /deleteCredential\(id, credentialId\).*method: 'DELETE'/, 'credential delete API mapping is missing')
assert.match(externalAccess, /externalAccessAPI\.revokeCredential/, 'credential revoke action is missing')
assert.match(externalAccess, /externalAccessAPI\.deleteCredential/, 'credential delete action is missing')
assert.match(externalAccess, /function CallableResources/, 'external endpoint inventory is missing')
assert.match(externalAccess, /permission\.resources/, 'permission editor does not expose its concrete endpoints')
for (const route of ['orders/:order_id/emails', 'mailboxes/:mailbox_ref/messages', 'emails/:message_id/body', 'emails/:message_id/attachments/:index']) {
  assert.match(externalAccess, new RegExp(route.replaceAll('/', '\\/')), `external endpoint inventory is missing ${route}`)
}
assert.doesNotMatch(externalAccess, /group === '过滤规则' \? 'filter'/, 'retired legacy filter permissions are still grouped in external access')
assert.match(mailboxes, /mailboxes\.list\.jumpAria/, 'mailbox direct page navigation is missing')
assert.match(emails, /emails\.list\.jumpAria/, 'email direct page navigation is missing')

assert.match(filters, /const TABS = \['overview', 'manual', 'ad', 'decisions', 'quarantines', 'legacy'\]/, 'policy page must expose all six tabs')
assert.match(filters, /<LegacyFiltersPage \/>/, 'legacy filter panel is missing')
assert.match(filters, /function RevisionInsights/, 'revision diff and validation panel is missing')
assert.match(filters, /filterPolicy\.adActionPreview/, 'pre-publish action surface preview is missing')
for (const method of ['manualRevisions', 'createManual', 'validateManual', 'publishManual', 'adRevisions', 'createAd', 'validateAd', 'publishAd', 'decisions', 'decision', 'quarantines', 'quarantineMessage', 'releaseQuarantine', 'allowAndReleaseQuarantine', 'confirmQuarantineAd']) {
  assert.match(api, new RegExp(`\\b${method}\\(`), `filter policy API is missing ${method}`)
}

console.log('UI contract check passed: policy lifecycle, legacy access, server endpoints, credentials, and pagination')
