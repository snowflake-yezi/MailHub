export const TEMPLATE_LANGUAGES = ['curl', 'javascript', 'python']

const RESOURCE_DESCRIPTIONS = {
  'POST /api/v1/mailboxes': 'createMailbox',
  'GET /api/v1/mailboxes/:mailbox_ref': 'readMailbox',
  'POST /api/v1/mailboxes/:mailbox_ref/disable': 'disableMailbox',
  'GET /api/v1/orders/:order_id/emails': 'listEmailsByOrder',
  'GET /api/v1/mailboxes/:mailbox_ref/messages': 'listEmailsByMailbox',
  'GET /api/v1/emails/:message_id/body': 'readEmailBody',
  'GET /api/v1/emails/:message_id/attachments/:index': 'downloadAttachment',
  'GET /api/v1/emails/:message_id/raw': 'downloadRawEmail',
}

const MANUAL_RULE_EXAMPLE = {
  logical_id: 'rule-airline-notice',
  name: 'Allow airline notices',
  scope_type: 'global',
  scope_id: null,
  action: 'allow',
  priority: 10,
  mode: 'shadow',
  source: 'external',
  conditions: [
    { field: 'header_from.domain', operator: 'eq', value: 'airline.example', negated: false, position: 0 },
  ],
}

const AD_DETECTOR_EXAMPLE = {
  logical_id: 'detector-airline-promo',
  name: 'Airline promotion detector',
  symbol: 'AIRLINE_PROMO',
  mode: 'shadow',
  source: 'external',
  source_reference: 'campaign-policy-v1',
  conditions: [
    { field: 'subject', operator: 'contains', value: 'special offer', negated: false, position: 0 },
  ],
}

const AD_COMPOSITE_EXAMPLE = {
  logical_id: 'composite-airline-ad',
  name: 'Airline advertising score',
  symbol: 'AIRLINE_AD',
  mode: 'shadow',
  score_policy: 'max',
  all_of: [],
  any_of: ['AIRLINE_PROMO'],
  none_of: [],
}

const REQUEST_BODY_RULES = [
  { key: 'POST /api/v1/mailboxes', body: { order_id: 'ORDER-20260703-001', domain_id: 1, retention_days: 30 } },
  { key: 'POST /api/v1/manual-filter-revisions', body: { base_revision: 7 } },
  { key: 'POST /api/v1/ad-filter-revisions', body: { base_revision: 7 } },
  { pattern: /^POST \/api\/v1\/manual-filter-revisions\/:revision\/rules$/, body: MANUAL_RULE_EXAMPLE },
  { pattern: /^PUT \/api\/v1\/manual-filter-revisions\/:revision\/rules\/:logical_id$/, body: MANUAL_RULE_EXAMPLE },
  { pattern: /^POST \/api\/v1\/ad-filter-revisions\/:revision\/detectors$/, body: AD_DETECTOR_EXAMPLE },
  { pattern: /^PUT \/api\/v1\/ad-filter-revisions\/:revision\/detectors\/:logical_id$/, body: AD_DETECTOR_EXAMPLE },
  { pattern: /^POST \/api\/v1\/ad-filter-revisions\/:revision\/composites$/, body: AD_COMPOSITE_EXAMPLE },
  { pattern: /^PUT \/api\/v1\/ad-filter-revisions\/:revision\/composites\/:logical_id$/, body: AD_COMPOSITE_EXAMPLE },
  { pattern: /^PUT \/api\/v1\/ad-filter-revisions\/:revision\/weights\/:symbol$/, body: { score: 4.25 } },
]

const QUERY_PARAMETER_RULES = [
  {
    pattern: /^GET \/api\/v1\/(orders\/:order_id\/emails|mailboxes\/:mailbox_ref\/messages)$/,
    parameters: [
      { name: 'page', required: false, example: '1' },
      { name: 'size', required: false, example: '20' },
    ],
  },
  {
    pattern: /^GET \/api\/v1\/emails\/:message_id\/(body|raw|attachments\/:index)$/,
    parameters: [{ name: 'mailbox', required: true, example: 'order-xxx@example.com' }],
  },
]

function resourceKey(resource) {
  return `${resource.method} ${resource.path}`
}

function parameterExample(name, path) {
  const examples = {
    order_id: 'ORDER-20260703-001',
    message_id: '<message-id@example.com>',
    index: '0',
    revision: '7',
    logical_id: 'rule-airline-notice',
    symbol: 'AIRLINE_PROMO',
  }
  if (name === 'mailbox_ref') {
    return path.endsWith('/messages') ? 'order-xxx@example.com' : 'ORDER-20260703-001'
  }
  return examples[name] || `example-${name}`
}

function pathParameters(resource) {
  return [...resource.path.matchAll(/:([A-Za-z0-9_]+)/g)].map(match => ({
    name: match[1],
    location: 'path',
    required: true,
    example: parameterExample(match[1], resource.path),
  }))
}

function queryParameters(resource) {
  const key = resourceKey(resource)
  const rule = QUERY_PARAMETER_RULES.find(item => item.pattern.test(key))
  return (rule?.parameters || []).map(parameter => ({ ...parameter, location: 'query' }))
}

function requestBody(resource) {
  const key = resourceKey(resource)
  const rule = REQUEST_BODY_RULES.find(item => item.key === key || item.pattern?.test(key))
  return rule?.body || null
}

function responseKind(resource) {
  if (resource.path.includes('/attachments/:index')) return 'binary'
  if (resource.path.endsWith('/raw')) return 'eml'
  return 'json'
}

function extraHeaders(resource) {
  return resource.path.endsWith('/publish') ? { 'Idempotency-Key': 'publish-request-001' } : {}
}

export function buildResourceReference(resource, t) {
  const key = resourceKey(resource)
  const descriptionKey = RESOURCE_DESCRIPTIONS[key]
  const name = resource.displayName || resource.name
  return {
    key,
    description: descriptionKey
      ? t(`externalAccess.resources.descriptions.${descriptionKey}`)
      : t(`externalAccess.permissions.descriptions.${resource.permission_code.replace(':', '_')}`, {
        defaultValue: resource.permission?.description || t('externalAccess.resources.reference.defaultDescription', { name }),
      }),
    parameters: [...pathParameters(resource), ...queryParameters(resource)],
    requestBody: requestBody(resource),
    responseKind: responseKind(resource),
    extraHeaders: extraHeaders(resource),
  }
}

function sampleURL(resource, reference, baseURL) {
  let path = resource.path
  for (const parameter of reference.parameters.filter(item => item.location === 'path')) {
    path = path.replace(`:${parameter.name}`, encodeURIComponent(parameter.example))
  }
  const query = reference.parameters.filter(item => item.location === 'query')
  const queryString = query.map(item => `${encodeURIComponent(item.name)}=${encodeURIComponent(item.example)}`).join('&')
  return `${baseURL.replace(/\/$/, '')}${path}${queryString ? `?${queryString}` : ''}`
}

function requestHeaders(reference) {
  return {
    Authorization: 'Bearer <token>',
    ...(reference.requestBody ? { 'Content-Type': 'application/json' } : {}),
    ...reference.extraHeaders,
  }
}

function curlTemplate(resource, reference, url) {
  const lines = [
    `curl --request ${resource.method} \\`,
    `  --url '${url}' \\`,
  ]
  const headers = Object.entries(requestHeaders(reference))
  headers.forEach(([name, value], index) => {
    const hasMore = index < headers.length - 1 || reference.requestBody
    lines.push(`  --header '${name}: ${value}'${hasMore ? ' \\' : ''}`)
  })
  if (reference.requestBody) {
    lines.push(`  --data '${JSON.stringify(reference.requestBody, null, 2)}'`)
  }
  if (reference.responseKind !== 'json') {
    lines[lines.length - 1] += ' \\'
    lines.push(`  --output '${reference.responseKind === 'eml' ? 'message.eml' : 'attachment.bin'}'`)
  }
  return lines.join('\n')
}

function javascriptTemplate(resource, reference, url) {
  const options = [
    `  method: '${resource.method}',`,
    `  headers: ${JSON.stringify(requestHeaders(reference), null, 2).replaceAll('\n', '\n  ')},`,
  ]
  if (reference.requestBody) {
    options.push(`  body: JSON.stringify(${JSON.stringify(reference.requestBody, null, 2).replaceAll('\n', '\n  ')}),`)
  }
  const resultLine = reference.responseKind === 'json'
    ? 'const result = await response.json()'
    : 'const result = await response.blob()'
  return [
    `const response = await fetch('${url}', {`,
    ...options,
    '})',
    '',
    "if (!response.ok) throw new Error(`HTTP ${response.status}`)",
    resultLine,
    'console.log(result)',
  ].join('\n')
}

function pythonTemplate(resource, reference, url) {
  const lines = ['import requests']
  if (reference.requestBody) lines.push('import json')
  lines.push('', `url = '${url}'`, `headers = ${JSON.stringify(requestHeaders(reference), null, 2)}`)
  if (reference.requestBody) {
    lines.push(`payload = json.loads('''${JSON.stringify(reference.requestBody, null, 2)}''')`)
  }
  lines.push('', 'response = requests.request(', `    '${resource.method}',`, '    url,', '    headers=headers,')
  if (reference.requestBody) lines.splice(lines.length - 1, 0, '    json=payload,')
  lines.push(')')
  lines.push('response.raise_for_status()')
  lines.push(reference.responseKind === 'json' ? 'result = response.json()' : 'result = response.content')
  lines.push('print(result)')
  return lines.join('\n')
}

export function buildCallTemplate(resource, reference, language, baseURL) {
  const url = sampleURL(resource, reference, baseURL)
  if (language === 'javascript') return javascriptTemplate(resource, reference, url)
  if (language === 'python') return pythonTemplate(resource, reference, url)
  return curlTemplate(resource, reference, url)
}
