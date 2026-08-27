# Account Linker

A Go 1.24 account-linking CLI with deterministic batch and incremental modes.
Every `verified_distinct` relationship is an absolute exclusion.

## Build and test

The project uses only the Go standard library.

```bash
go test ./...
go test -race ./...
go build -mod=vendor -o bin/linker ./cmd/linker
```

All tests are kept in `cmd/linker/main_test.go`.

## Hyperparameters

All tunable values for the frequency-aware experiment are in
`internal/similarity/config.go`, inside `DefaultConfig()`. Edit that function,
then rerun the tests and build.

```go
func DefaultConfig() Config {
    return Config{
        Smoothing:      1,
        EvidenceScale:  0.75,
        MergeThreshold: 0.60,
        StrongPairThreshold: 0.75,
        LinkageRule:    CompleteLinkage,
        IPHighFactor:   0.45,
        IPMidFactor:    0.10,
        TimeEvidence:   0.25,
        // IP prefixes, time decay, and blocking limits follow.
    }
}
```

| Parameter group | Effect |
| --- | --- |
| `Smoothing` | Stabilizes `-log` rarity estimates for small datasets. |
| `EvidenceScale` | Controls the saturation mapping from raw evidence to `[0,1]`. |
| `MergeThreshold` | Lower values increase recall and false-positive risk; higher values increase precision. |
| `StrongPairThreshold`, `LinkageRule` | Configure the optional average-linkage comparison. |
| `IPHighFactor`, `IPMidFactor` | Make `/24` and `/16` evidence weaker than exact IP evidence. |
| `TimeEvidence`, `TimeDecay` | Keep timestamp proximity as a small, smoothly decaying contribution. |
| Email parameters | Control only n-gram candidate generation; email score strength comes from similarity and rarity. |
| `MaxBlockSize`, `MaxCandidates` | Bound candidate work and streaming latency on common signals. |

Missing values contribute no evidence. There are no independent field weights.
Batch scores every unordered pair; blocking parameters affect only streaming.

## Part 1: batch linking

```bash
bin/linker link \
  --accounts ../202608-intern-takehome-assignment/datasets/sample_accounts.jsonl \
  --constraints ../202608-intern-takehome-assignment/datasets/sample_constraints.jsonl \
  --output clusters.json
```

The output is one JSON document:

```json
{
  "clusters": [
    {
      "cluster_id": "c1",
      "account_ids": ["acc_1", "acc_7"],
      "confidence": 0.91
    }
  ]
}
```

Every input account appears exactly once. The default complete-linkage rule
requires every all-pairs cross-cluster score to reach the threshold and forbids
every `verified_distinct` merge.

## Part 2: incremental streaming

Start the process with the initial dataset:

```bash
bin/linker stream \
  --accounts ../202608-intern-takehome-assignment/datasets/sample_accounts.jsonl \
  --constraints ../202608-intern-takehome-assignment/datasets/sample_constraints.jsonl
```

Then write one account JSON object per line to stdin:

```json
{"account_id":"acc_new01","email":"thao.miller@proton.me","device_id":"dev_7fc754","ip":"160.77.64.134","payment_fingerprint":"pf_c2c964","created_at":"2026-04-18T10:43:03Z"}
```

The program immediately flushes one assignment line to stdout:

```json
{"account_id":"acc_new01","cluster_id":"c1","confidence":0.87}
```

Streaming initializes clusters once with the batch algorithm. Each new account
uses the maintained indexes, evaluates every member of each candidate cluster,
joins the strongest valid cluster, or creates a deterministic singleton without
rerunning the full batch pipeline. It is scored against the current frequency
state, then its values are added to the frequency tables after assignment.

## Offline evaluation

`cmd/evaluate` is separate from production linking and is the only command that
reads ground truth. Evaluate an existing clusters file:

```bash
go run ./cmd/evaluate \
  --clusters clusters.json \
  --truth ../202608-intern-takehome-assignment/datasets/sample_truth.json
```

For an offline threshold experiment, it can also run batch linking directly:

```bash
go run ./cmd/evaluate \
  --accounts ../202608-intern-takehome-assignment/datasets/sample_accounts.jsonl \
  --constraints ../202608-intern-takehome-assignment/datasets/sample_constraints.jsonl \
  --truth ../202608-intern-takehome-assignment/datasets/sample_truth.json \
  --threshold 0.60
```

Use `--linkage average-strong --strong-threshold 0.75` to reproduce the
alternative rule. The evaluator reports F2, fraud-ring recovery, affected
legitimate actors, business cost, constraints, and streaming candidate recall
in addition to pairwise metrics. Truth is never used by `cmd/linker`.
