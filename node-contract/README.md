# Node Contract

`node-contract` is the versioned protocol shared by `mgmt-system` and
`mail-node`. Generated Go files are committed and must not be edited by hand.

Pinned tools are listed in `tools/versions.json`. Install exactly those
versions, then run the single canonical generation command from this module:

```text
go generate ./...
```

Go plugin installation commands for the current tool versions:

```text
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
```

Install `protoc` 28.3 from the official Protocol Buffers release and ensure all
three executables are on `PATH`. Verify a protocol change with:

```text
go generate ./...
go test ./...
```

Run `go generate ./...` a second time and require a clean diff before merging.
The compatibility test intentionally fails whenever the descriptor changes;
review field numbers, enum values, stream direction, route constants and type
names before updating its expected digest.
