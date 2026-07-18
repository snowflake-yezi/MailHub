import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const sourceDirectory = path.resolve(scriptDirectory, '../src')

async function source(relativePath) {
  return readFile(path.join(sourceDirectory, relativePath), 'utf8')
}

const [api, servers, externalAccess, mailboxes, emails] = await Promise.all([
  source('api.js'),
  source('pages/ServersPage.jsx'),
  source('pages/ExternalAccessPage.jsx'),
  source('pages/MailboxesPage.jsx'),
  source('pages/EmailsPage.jsx'),
])

for (const field of ['smtp_host', 'imap_host']) {
  assert.match(servers, new RegExp(`value=\\{form\\.${field}\\}`), `server editor is missing ${field}`)
  assert.match(servers, new RegExp(`${field}: form\\.${field}`), `server update payload is missing ${field}`)
}

assert.match(api, /revokeCredential\(id, credentialId\).*\/revoke.*method: 'POST'/, 'credential revoke API mapping is missing')
assert.match(api, /deleteCredential\(id, credentialId\).*method: 'DELETE'/, 'credential delete API mapping is missing')
assert.match(externalAccess, /externalAccessAPI\.revokeCredential/, 'credential revoke action is missing')
assert.match(externalAccess, /externalAccessAPI\.deleteCredential/, 'credential delete action is missing')
assert.match(mailboxes, /mailboxes\.list\.jumpAria/, 'mailbox direct page navigation is missing')
assert.match(emails, /emails\.list\.jumpAria/, 'email direct page navigation is missing')

console.log('UI contract check passed: server endpoints, credential lifecycle, and direct pagination')
