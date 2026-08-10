import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const sourceDirectory = path.resolve(scriptDirectory, '../src')

async function source(relativePath) {
  return readFile(path.join(sourceDirectory, relativePath), 'utf8')
}

const [api, servers, externalAccess, externalApiReference, mailboxes, emails, filters, legacyFilters] = await Promise.all([
  source('api.js'),
  source('pages/ServersPage.jsx'),
  source('pages/ExternalAccessPage.jsx'),
  source('externalApiReference.js'),
  source('pages/MailboxesPage.jsx'),
  source('pages/EmailsPage.jsx'),
  source('pages/FiltersPage.jsx'),
  source('pages/LegacyFiltersPage.jsx'),
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
assert.match(externalAccess, /api-explorer-grid/, 'external endpoint inventory is not using the two-column explorer')
assert.match(externalAccess, /TEMPLATE_LANGUAGES\.map/, 'external endpoint details do not expose template languages')
assert.match(externalAccess, /buildResourceReference/, 'external endpoints cannot render reference details')
assert.match(externalAccess, /copyTemplate/, 'external call templates cannot be copied')
assert.match(externalApiReference, /export function buildCallTemplate/, 'external call template generator is missing')
for (const language of ['curlTemplate', 'javascriptTemplate', 'pythonTemplate']) {
  assert.match(externalApiReference, new RegExp(`function ${language}`), `external API reference is missing ${language}`)
}
for (const route of ['orders/:order_id/emails', 'mailboxes/:mailbox_ref/messages', 'emails/:message_id/body', 'emails/:message_id/attachments/:index']) {
  assert.match(externalAccess, new RegExp(route.replaceAll('/', '\\/')), `external endpoint inventory is missing ${route}`)
}
for (const route of ['manual-filter-revisions/:revision/rules/:logical_id', 'manual-filter-revisions/:revision/publish', 'ad-filter-revisions/:revision/detectors/:logical_id', 'ad-filter-revisions/:revision/composites/:logical_id', 'ad-filter-revisions/:revision/weights/:symbol', 'ad-filter-revisions/:revision/publish']) {
  assert.match(externalAccess, new RegExp(route.replaceAll('/', '\\/')), `external endpoint reference is missing ${route}`)
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
assert.match(emails, /buildEmailPreviewCSP\(window\.location\.origin\)/, 'srcdoc CSP must allow safe remote images automatically')
assert.match(emails, /sanitizeLegacyBackgroundImages\(doc\)/, 'legacy background images must load automatically')
assert.match(emails, /const remoteURL = normalizeRemoteImageURL\(src\)\s+if \(remoteURL\) \{\s+img\.setAttribute\('src', remoteURL\)/s, 'remote image sources must load automatically')
assert.match(emails, /allow-popups allow-popups-to-escape-sandbox/, 'safe email links must open outside the preview sandbox')
assert.match(emails, /setAttribute\('rel', 'noopener noreferrer'\)/, 'email links must not retain an opener or referrer')
assert.doesNotMatch(emails, /remoteImagesAllowed|loadRemoteImages/, 'remote images must not require a per-message loading control')
assert.match(emails, /emailAPI\.rawUrl\(detail\.message_id, query\)/, 'email details cannot download the original EML')
assert.doesNotMatch(emails, /\['html', 'HTML'\]|rawMeta/, 'legacy Text/HTML/Raw metadata tabs are still exposed')

for (const method of ['invitations', 'createInvitation', 'revokeInvitation', 'deleteInvitation', 'requests', 'approve', 'reject', 'credentials', 'rotateCredential', 'revokeCredentials', 'revokeCredential', 'deleteCredential', 'disconnect']) {
  assert.match(api, new RegExp(`\\b${method}\\(`), `node enrollment API is missing ${method}`)
}
for (const component of ['InvitationDrawer', 'RequestDialog', 'CredentialDialog', 'SecretDialog']) {
  assert.match(servers, new RegExp(`function ${component}\\b`), `server pool is missing ${component}`)
}
assert.match(servers, /nodeEnrollmentAPI\.createInvitation/, 'server pool cannot create enrollment invitations')
assert.match(servers, /nodeEnrollmentAPI\.approve/, 'server pool cannot approve enrollment requests')
assert.match(servers, /nodeEnrollmentAPI\.rotateCredential/, 'server pool cannot rotate node credentials')
assert.match(servers, /nodeEnrollmentAPI\.revokeCredential/, 'server pool cannot end a node credential rotation overlap')
assert.match(servers, /nodeEnrollmentAPI\.deleteCredential/, 'server pool cannot delete revoked or expired node credentials')
assert.match(servers, /nodeEnrollmentAPI\.deleteInvitation/, 'server pool cannot delete revoked enrollment invitations')
assert.match(servers, /invitation\.state === 'revoked'/, 'invitation deletion must be limited to revoked invitations')
assert.match(servers, /nodeEnrollmentAPI\.disconnect/, 'server pool cannot disconnect an active node session')
assert.match(servers, /server\.connection_state !== 'connected'/, 'node disconnect action must be disabled without an active session')
assert.doesNotMatch(servers, /setInvitations\([^)]*\.token/, 'one-time enrollment token must not enter invitation list state')

assert.match(filters, /const TABS = \['overview', 'manual', 'ad', 'decisions', 'quarantines', 'legacy'\]/, 'policy page must expose all six tabs')
assert.match(filters, /<LegacyFiltersPage \/>/, 'legacy filter panel is missing')
assert.match(filters, /function RevisionInsights/, 'revision diff and validation panel is missing')
assert.match(filters, /filterPolicy\.adActionPreview/, 'pre-publish action surface preview is missing')
for (const group of ['fields', 'operators', 'actions', 'modes', 'booleanValues', 'scorePolicies', 'groups']) {
  assert.match(filters, new RegExp(`optionLabel\\(t, '${group}'`), `filter policy ${group} are not localized`)
}
assert.match(filters, /<option key=\{value\} value=\{value\}>\{optionLabel\(t, 'actions', value\)\}<\/option>/, 'localized action labels must preserve API values')
assert.match(filters, /conditionSummary\(t, condition\)/, 'condition summaries are not localized')
assert.doesNotMatch(filters, /<option key=\{value\}>\{value\}<\/option>/, 'filter policy still exposes raw enum options')
assert.match(legacyFilters, /filters\.actionLabels\.\$\{rule\.action\}/, 'legacy rule actions are not localized')
for (const method of ['manualRevisions', 'createManual', 'validateManual', 'publishManual', 'adRevisions', 'createAd', 'validateAd', 'publishAd', 'decisions', 'decision', 'quarantines', 'quarantineMessage', 'releaseQuarantine', 'allowAndReleaseQuarantine', 'confirmQuarantineAd']) {
  assert.match(api, new RegExp(`\\b${method}\\(`), `filter policy API is missing ${method}`)
}

console.log('UI contract check passed: policy lifecycle, legacy access, server endpoints, credentials, and pagination')
