# Filter contract v1

This directory is the cross-service compatibility baseline for the filter
redesign. Runtime integration starts after S0; these fixtures do not change
legacy filtering behavior.

## Canonical JSON

- `schema_version` is the integer `1`.
- UTF-8 is required. JSON is encoded without insignificant whitespace or HTML
  escaping.
- Object fields use the order defined by the Go DTO. Map keys are sorted.
- Semantically unordered arrays are sorted by their stable IDs or symbols.
  Condition arrays are sorted by `position`.
- Missing collections are encoded as `[]`. Optional scalar/object values are
  encoded as explicit `null`; fields are not omitted.
- Scores are signed decimal numbers backed by integer thousandths. They use at
  most three decimal places and no exponent, leading plus, trailing zero, or
  negative-zero representation.
- Bundle `checksum` is lowercase SHA-256 over the canonical bundle after
  replacing its checksum with the empty string.
- Times use UTC RFC 3339 with the minimum precision needed by the value.
- Unknown object fields are rejected.

## Errors

Contract validation uses stable machine codes:

- `contract_invalid_json`
- `contract_invalid_schema_version`
- `contract_required`
- `contract_invalid_enum`
- `contract_invalid_value`
- `contract_checksum_mismatch`

Parser and delivery failures use separate domain error codes. Human-readable
messages are diagnostic text and are not API contracts.

## Golden cases

`eml/` contains synthetic messages under reserved `.example` domains. The
fixture body, addresses, IDs, and URLs are not copied from production.
`golden-cases.json` freezes the expected normalized features and decisions for
S2-S6. S0 validates the contract and MessageKey inputs; later phases replace
those structural checks with parser and engine replay assertions.

The attachment boundary fixture is 4 KiB in v1. It is a deterministic test
boundary, not the production attachment or message limit.
