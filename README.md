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

All tunable values for the Fellegi-Sunter experiment are in
`internal/similarity/config.go`, inside `DefaultConfig()`. Edit that function,
then rerun the tests and build.

```go
func DefaultConfig() Config {
    return Config{
        Smoothing:             0.5,
        LinkEvidenceThreshold:   3.0,
        StrongEvidenceThreshold: 3.0,
        LinkageRule:              AverageStrongLinkage,
        EmailM:                [4]float64{0.35, 0.30, 0.20, 0.15},
        DeviceM:               [2]float64{0.55, 0.45},
        // Payment, IP, time, bucket, and blocking parameters follow.
    }
}
```

| Parameter group | Effect |
| --- | --- |
| `Smoothing` | Prevents zero empirical probabilities and infinite log ratios. |
| `LinkEvidenceThreshold` | Raw log-evidence required by complete-linkage; lower values favor recall. |
| `StrongEvidenceThreshold`, `LinkageRule` | Configure the average-linkage strong-pair guard. |
| `*M` arrays | Semi-empirical `P(agreement level | same actor)` assumptions. |
| Email/time boundaries | Define the small set of agreement buckets. |
| IP prefixes | Define exact, high-subnet, and mid-subnet agreement. |
| `MaxBlockSize`, `MaxCandidates` | Bound candidate work and streaming latency on common signals. |

The scorer estimates `u = P(level | different actor)` from deterministic sampled
pairs in the initial account dataset. Missing values contribute no evidence.
Batch scores all unordered pairs. Blocking parameters affect only streaming;
membership always requires the raw threshold and all constraint checks.

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

Every input account appears exactly once. The selected rule requires average
cross-cluster raw evidence at `3.0`, at least one cross pair at `3.0`, and no
`verified_distinct` pair. Complete linkage remains available for comparison.

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
rerunning the full batch pipeline. Fellegi-Sunter parameters remain fixed after
initialization; only account and candidate indexes are updated.

## Offline evaluation

Ground truth is read only by the separate evaluation command:

```bash
go run ./cmd/evaluate \
  --accounts ../202608-intern-takehome-assignment/datasets/sample_accounts.jsonl \
  --constraints ../202608-intern-takehome-assignment/datasets/sample_constraints.jsonl \
  --truth ../202608-intern-takehome-assignment/datasets/sample_truth.json \
  --threshold 3.0 --strong-threshold 3.0 --linkage average-strong
```

It also reports F2, fraud-ring recovery, affected legitimate actors, business
cost, and streaming candidate recall. `cmd/linker` never reads truth.
