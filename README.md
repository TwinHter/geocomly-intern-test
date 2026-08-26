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
        EmailWeight:        0.50,
        DeviceWeight:       0.175,
        PaymentWeight:      0.175,
        IPWeight:           0.10,
        TimeWeight:         0.05,
        MissingValueScore:  0.20,
        MergeThreshold:     0.44,
        NeutralSimilarity:  0.45,
        // Other email, IP, time, and blocking parameters follow.
    }
}
```

The five signal weights should sum to `1.0` so the final score remains
normalized to `[0, 1]`.

| Parameter group | Effect |
| --- | --- |
| `*Weight` | Relative contribution of email, device, payment, IP, and time. |
| `MergeThreshold` | Retained baseline setting; correlation decisions do not use it. |
| `NeutralSimilarity` | Centers baseline similarity into signed correlation edges. |
| `MissingValueScore` | Evidence assigned when a signal is unknown; weights are not renormalized. |
| Email parameters | Control local/domain scoring and n-gram candidate generation. |
| IP/time parameters | Control subnet and timestamp-distance similarity buckets. |
| `MaxBlockSize`, `MaxCandidates` | Bound candidate work and streaming latency on common signals. |

`NeutralSimilarity` is the only parameter added by this experiment. Baseline
field scoring remains unchanged. Blocking parameters affect streaming candidate
generation; batch correlation clustering precomputes all pair scores.

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

Every input account appears exactly once. Batch starts from singletons and
repeatedly accepts the legal cluster merge with the largest positive sum of
cross-cluster signed edges. `verified_distinct` always forbids a merge.

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
uses the maintained indexes and joins the legal candidate cluster with the
largest positive insertion gain, or creates a deterministic singleton. It does
not globally recluster historical accounts.

## Offline evaluation

Ground truth is read only by the separate evaluator:

```bash
go run ./cmd/evaluate \
  --accounts ../202608-intern-takehome-assignment/datasets/sample_accounts.jsonl \
  --constraints ../202608-intern-takehome-assignment/datasets/sample_constraints.jsonl \
  --truth ../202608-intern-takehome-assignment/datasets/sample_truth.json \
  --neutral 0.45
```

It reports TP/FP/TN/FN, pairwise metrics, clusters, singletons, and constraint
violations. `cmd/linker` never reads truth.
