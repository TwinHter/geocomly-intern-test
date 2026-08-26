# Account Linker

This Go 1.24 command links likely related accounts while treating every `verified_distinct` pair as an absolute exclusion.

## Build and test

```bash
go test ./...
go build -mod=mod -o bin/linker ./cmd/linker
```

The module uses only the Go standard library.
All tunable parameters are centralized in `internal/similarity/config.go`, and all tests are in `cmd/linker/main_test.go`.

## Run

```bash
bin/linker link --accounts accounts.jsonl --constraints constraints.jsonl --output clusters.json
bin/linker stream --accounts accounts.jsonl --constraints constraints.jsonl
```

`stream` reads account JSONL from stdin and immediately flushes one assignment JSON object per line. Batch mode greedily merges the highest-scoring candidate pairs only when every cross-cluster pair passes the threshold and has no constraint. Streaming mode uses the same score and indexes, checks every member of each candidate cluster, and selects the strongest valid cluster.
