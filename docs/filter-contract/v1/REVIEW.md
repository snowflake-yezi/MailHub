# S0 technical review record

Date: 2026-07-20

Scope: filter contract v1, synthetic EML fixtures, golden results, and the
mail-node contract implementation. This is a technical baseline review; it is
not approval of production weights, thresholds, labels, or automatic
quarantine.

## Decisions

- Both bundles, decisions, outbox events, and release receipts carry integer
  `schema_version: 1` and reject unknown fields.
- Scores use signed integer thousandths in Go and canonical decimal JSON. This
  avoids float-dependent bundle checksums while retaining the documented
  three-decimal limit.
- Bundle checksums cover canonical payload bytes with the checksum field set to
  an empty string. Collections use explicit arrays and optional values use
  explicit `null`.
- Manual and ad bundle revisions remain independent. Fixture revisions 7 and
  13 are synthetic contract examples and are never loaded at startup.
- The ad fixture is entirely shadow. Its decision records a would-quarantine
  score while the real action remains allow, proving shadow has no action side
  effect at the contract level.
- MessageKey fixtures use server 7, `inbox@tenant.example`, the Maildir name
  without `:2,flags`, and the checked-out EML byte size. `.gitattributes`
  fixes fixture line endings to LF across platforms.
- The 4 KiB attachment is only a deterministic fixture boundary. S2 must set
  and review the production parser limits separately.

## Fixture coverage

- Labels: ad, transactional, other, uncertain.
- MIME and parser cases: malformed multipart, duplicate headers, multiple
  URLs, multipart related inline image, and exact 4 KiB attachment content.
- Golden output: MessageKey, normalized features, parse warnings, symbol
  evidence, suppression, contribution, score, shadow action, and final action.
- Privacy: all domains use `.example`; a test rejects common credential
  markers in JSON and EML fixtures.

## Verification

Executed from the repository on 2026-07-20:

```text
cd mail-node && go test ./...
cd mgmt-system && go test ./...
cd mgmt-system/web && npm test
cd mgmt-system/web && npm run build
```

All commands passed. The contract tests additionally verify exact canonical
bytes and checksums, strict unknown-field rejection, stable MessageKey flags,
fixture inventory and sizes, schema closure, deterministic bundle ordering,
and the decoded 4096-byte attachment payload.

## Deferred boundaries

- S2 must prove parser output against these goldens and document any reviewed
  contract correction before v1 is consumed in production.
- S4 must implement whole-bundle semantic validation, including condition
  matrices, composite reference checks, DAG depth/cycles, and revision state.
- Production weights, thresholds, sampling, and business acceptance remain S11
  work and are not implied by this review.
