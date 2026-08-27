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

All tunable values are centralized in `internal/similarity/config.go` inside
`DefaultConfig()`. Edit that function, then rerun the tests and build.

```go
func DefaultConfig() Config {
    return Config{
        EmailWeight:         0.40,
        DeviceWeight:        0.25,
        PaymentWeight:       0.25,
        IPWeight:            0.08,
        TimeWeight:          0.02,
        MergeThreshold:      0.45,
        StrongPairThreshold: 0.80,
        LinkageRule:         CompleteLinkage,
        // Other email, IP, time, and blocking parameters follow.
    }
}
```

The five signal weights should sum to `1.0` so the final score remains
normalized to `[0, 1]`.

| Parameter group | Effect |
| --- | --- |
| `*Weight` | Relative contribution of email, device, payment, IP, and time. |
| `MergeThreshold` | Lower values increase recall and false-positive risk; higher values increase precision. |
| `StrongPairThreshold`, `LinkageRule` | Configure the optional average-linkage experiment. |
| Email parameters | Control local-part fuzzy scoring, exact-domain bonus, and streaming n-grams. |
| IP/time parameters | Control subnet and timestamp-distance similarity buckets. |
| `MaxBlockSize`, `MaxCandidates` | Bound candidate work and streaming latency on common signals. |

Missing fields add no evidence. The weighted score is divided by the weights of
fields present in both records; timestamp alone is not an identity signal.
Batch scores all unordered pairs. Blocking parameters affect only streaming.

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

Every input account appears exactly once. Batch scores all pairs, then complete
linkage requires every cross-cluster pair to reach the threshold and rejects
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
rerunning the full batch pipeline.

## Offline evaluation

Ground truth is used only by the separate evaluator:

```bash
go run ./cmd/evaluate \
  --accounts ../202608-intern-takehome-assignment/datasets/sample_accounts.jsonl \
  --constraints ../202608-intern-takehome-assignment/datasets/sample_constraints.jsonl \
  --truth ../202608-intern-takehome-assignment/datasets/sample_truth.json \
  --linkage complete --threshold 0.45
```

It reports pairwise metrics, F2, fraud-ring recovery, affected legitimate
actors, business cost, constraint violations, and streaming candidate recall.
