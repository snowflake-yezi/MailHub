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

for (const field of ['smtp_host', 'imap_host', 'public_host', 'mail_public_ips']) {
  assert.match(servers, new RegExp(`value=\\{form\\.${field}\\}`), `server editor is missing ${field}`)
  assert.match(servers, new RegExp(`${field}: form\\.${field}`), `server update payload is missing ${field}`)
}
assert.match(servers, /mail_public_ips: form\.mail_public_ips\.split/, 'mail public IP payload is not normalized as a list')

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
assert.match(api, /rawUrl\(id, mailbox\).*\/raw\?mailbox=/s, 'admin raw EML URL mapping is missing')
assert.match(emails, /\['preview', t\('emails\.detail\.previewTab'\)\]/, 'email details must default to a message preview tab')
assert.match(emails, /detail\.html_body \? \(/, 'message preview must prefer the safe HTML renderer')
assert.match(emails, /: detail\.text_body \? \(/, 'message preview must fall back to plain text')
assert.match(emails, /partitionEmailAttachments\(detail\)/, 'message details must separate inline body images from file attachments')
assert.match(emails, /messages\.reduce\(\(sum, msg\) => sum \+ countFileAttachments\(msg\)/, 'email list counts must exclude inline body images')
assert.match(emails, /emailPresentation\.trailingInlineImages\.map/, 'unplaced inline images must render with the message body')
assert.match(emails, /emailPresentation\.fileAttachments\.map/, 'the attachment list must only render file attachments')
assert.match(emails, /emailAPI\.rawUrl\(detail\.message_id, query\)/, 'email details cannot download the original EML')
assert.doesNotMatch(emails, /\['html', 'HTML'\]|rawMeta/, 'legacy Text/HTML/Raw metadata tabs are still exposed')

for (const method of ['invitations', 'createInvitation', 'revokeInvitation', 'requests', 'approve', 'reject', 'credentials', 'rotateCredential', 'revokeCredentials']) {
  assert.match(api, new RegExp(`\\b${method}\\(`), `node enrollment API is missing ${method}`)
}
for (const component of ['InvitationDrawer', 'RequestDialog', 'CredentialDialog', 'SecretDialog']) {
  assert.match(servers, new RegExp(`function ${component}\\b`), `server pool is missing ${component}`)
}
assert.match(servers, /nodeEnrollmentAPI\.createInvitation/, 'server pool cannot create enrollment invitations')
assert.match(servers, /nodeEnrollmentAPI\.approve/, 'server pool cannot approve enrollment requests')
assert.match(servers, /nodeEnrollmentAPI\.rotateCredential/, 'server pool cannot rotate node credentials')
assert.doesNotMatch(servers, /setInvitations\([^)]*\.token/, 'one-time enrollment token must not enter invitation list state')

assert.match(filters, /const TABS = \['overview', 'manual', 'ad', 'decisions', 'quarantines', 'legacy'\]/, 'policy page must expose all six tabs')
assert.match(filters, /<LegacyFiltersPage \/>/, 'legacy filter panel is missing')
assert.match(filters, /function RevisionInsights/, 'revision diff and validation panel is missing')
assert.match(filters, /filterPolicy\.adActionPreview/, 'pre-publish action surface preview is missing')
for (const method of ['manualRevisions', 'createManual', 'validateManual', 'publishManual', 'adRevisions', 'createAd', 'validateAd', 'publishAd', 'decisions', 'decision', 'quarantines', 'quarantineMessage', 'releaseQuarantine', 'allowAndReleaseQuarantine', 'confirmQuarantineAd']) {
  assert.match(api, new RegExp(`\\b${method}\\(`), `filter policy API is missing ${method}`)
}

console.log('UI contract check passed: policy lifecycle, legacy access, server endpoints, credentials, and pagination')
