# Account-Linking Audit and Experiment Report

## 1. Scope

This report covers the final audited versions of four independent branches:

| Branch | Algorithm identity | Selected rule and parameter |
| --- | --- | --- |
| `main` | Weighted similarity | Complete linkage, threshold `0.45` |
| `exp/frequency-aware` | Rarity-weighted evidence | Complete linkage, threshold `0.60` |
| `exp/fellegi-sunter` | Fellegi-Sunter `log(m/u)` evidence | Average-strong linkage, raw thresholds `3.0/3.0` |
| `exp/correlation-clustering` | Signed-edge correlation-style clustering | Average edge support, neutral similarity `0.45` |

The assignment brief is the source of truth. Production code never reads
`sample_truth.json`; only `cmd/evaluate` uses it. No branch was merged into
another branch during this work.

## 2. Audit findings

No Critical issue was found. The following High issues were fixed.

| Branch | High issue | Fix and regression coverage |
| --- | --- | --- |
| `main` | Batch blocking could silently hide a true pair. | Batch now scores all unordered pairs; streaming keeps blocking. A no-shared-block regression proves the distinction. |
| `main` | Missing values contributed positive similarity. | Missing fields are omitted and available weights are renormalized; all-missing and timestamp-only pairs score zero. |
| `main` | Fuzzy domains and broad `/16` matches were too influential. | Fuzzy matching is local-part only, exact-domain bonus is `0.05`, fuzzy email is capped at `0.85`, and `/16` is `0.15`. |
| `exp/frequency-aware` | Batch blocking could hide true pairs. | Batch now scores and caches every pair; indexes remain streaming-only. |
| `exp/frequency-aware` | Broad subnet evidence was too strong. | `/24` and `/16` rarity factors were reduced to `0.45` and `0.10`. |
| `exp/fellegi-sunter` | Batch blocking could hide true pairs. | Batch now scores and caches every raw-evidence pair. |
| `exp/fellegi-sunter` | A rare `/16` used value-specific `u` and could look exact-like. | Only exact IP uses value rarity; subnet levels use level-level `u`, and `/16` has `m=0.05`. |
| `exp/fellegi-sunter` | Invalid `m` distributions could be silently floored during scoring. | Config validation requires positive probabilities summing to one and validates all bucket boundaries. |
| `exp/correlation-clustering` | The reused baseline gave positive evidence to missing fields. | It now uses the corrected available-field scorer. |
| `exp/correlation-clustering` | Raw cross-cluster sums favored large clusters. | Batch and streaming select by average signed-edge support. A regression graph distinguishes normalized `[[0,1],[2,3]]` from size-biased `[[0,1,3],[2]]`. |

Remaining known Medium/Low risks:

- Streaming candidate generation covers `31/32` true sample pairs (`96.875%`),
  so blocking can still miss an online link.
- Frequency statistics drift during streaming; prior assignments are not
  revisited.
- Fellegi-Sunter `m` probabilities are reasoned assumptions, not calibrated on
  held-out labels. Its initial `u` model remains fixed during streaming.
- Correlation batch mode is dense and greedy: `O(N^2)` memory and approximately
  `O(N^3)` agglomerative work. Streaming does not recluster history.
- The sample has only 100 accounts. Selection results may not generalize to the
  hidden dataset.

No known Critical or High issue remains after the fixes and validation below.

## 3. Normalization and field matching

### 3.1 Processing flow

For batch mode, records are validated and sorted by `account_id`, every
unordered pair is scored, and clustering applies the configured rule plus hard
constraints. For streaming, the initial data is batch-linked once, indexes
generate candidate clusters for each event, and every member required by the
cluster rule is checked. A shared block is only candidate generation; it never
guarantees a merge.

### 3.2 Email

Normalization is:

1. Trim surrounding whitespace.
2. Lowercase the complete value.
3. Split at the first `@`.
4. When `@` exists, remove the first `+` and the remainder of the local part.
5. Reassemble local and domain.

For example, ` Alice+shop@Example.COM ` becomes `alice@example.com`. No
provider-specific Gmail dot rewriting is used.

An exact normalized email scores `1.0`. A non-exact email uses Unicode-aware
Levenshtein similarity on the local parts only:

```text
local_similarity = 1 - edit_distance / max(local_rune_lengths)
fuzzy_email = 0.95 * local_similarity
            + 0.05 if domains are exactly equal
email_similarity = min(fuzzy_email, 0.85)
```

Domains are never fuzzy matched. Consequently, a shared `gmail.com` contributes
at most the small `0.05` bonus and cannot make unrelated local parts similar.
Missing email is unavailable, not agreement.

Streaming blocks on exact normalized email and 3-rune local-part n-grams.
Normally two n-grams must match; one is enough for local parts shorter than four
runes. Blocks above 256 accounts are ignored and an event retains at most 4,096
candidates.

### 3.3 Device and payment

Device ID and payment fingerprint are trimmed but remain case-sensitive opaque
identifiers. Their relationship is exact `1.0`, different `0.0`, or unavailable
when either side is empty/JSON `null`. There is no fuzzy comparison. Exact
trimmed values are streaming blocking keys.

### 3.4 IP

IP strings are trimmed, parsed with `net/netip`, canonicalized, and IPv4-mapped
addresses are unmapped. Invalid non-empty input is rejected during normal
record validation.

| Relationship | Baseline similarity |
| --- | ---: |
| Exact canonical IP | `1.00` |
| Same IPv4 `/24` or IPv6 `/64` | `0.45` |
| Same IPv4 `/16` or IPv6 `/48` | `0.15` |
| Different | `0.00` |
| Missing | unavailable |

Only the strongest relationship is used. Exact, `/24` or `/64`, and `/16` or
`/48` remain streaming keys; the broad key improves candidate recall but is
weak evidence.

### 3.5 Timestamp

`created_at` is parsed as RFC3339 into `time.Time`; comparison uses the absolute
elapsed duration, so equivalent instants with different UTC offsets match.

| Absolute difference | Similarity |
| --- | ---: |
| At most 1 hour | `1.00` |
| At most 24 hours | `0.80` |
| At most 7 days | `0.50` |
| At most 30 days | `0.20` |
| More than 30 days | `0.00` |
| Missing/zero | unavailable |

Time is not a blocking key and cannot be the only meaningful identity signal.

### 3.6 Weighted baseline score

```text
numerator = sum(weight_i * similarity_i) for fields present in both records
score     = numerator / sum(weight_i for those available fields)
```

If no identity field is comparable, score is zero. The weights are email
`0.40`, device `0.25`, payment `0.25`, IP `0.08`, and time `0.02`. Timestamp is
supporting evidence but cannot establish comparability by itself.

## 4. Branch-specific evidence and clustering

### 4.1 Weighted baseline

Candidate pairs are sorted by score. Complete linkage merges two components
only when every cross-pair score is at least `0.45` and no cross pair is
`verified_distinct`. Streaming selects the valid cluster with the highest
minimum member score.

The implemented alternative is average linkage with a strong-pair guard:

```text
average cross-pair score >= MergeThreshold
maximum cross-pair score >= StrongPairThreshold (fixed at 0.80)
no verified_distinct pair
```

Average linkage rescans deterministic component pairs because an average can
change after another merge; reusing the one-pass complete-linkage loop would be
order-dependent.

### 4.2 Frequency-aware

For value frequency `f`, account count `N`, and smoothing `s=1`:

```text
rarity(value) = -log((f+s)/(N+2s))
score = 1 - exp(-raw_evidence/(0.75 * maximum_rarity))
```

Exact email/device/payment/IP evidence uses rarity. Non-exact email uses only
local-part similarity times the smaller email rarity. Subnet evidence uses
rarity factors `0.45/0.10`; time contributes a small seven-day-decay term.
Missing contributes zero. The selected rule is complete linkage at `0.60`; the
alternative strong-pair threshold remained fixed at `0.75` during sweeps.
Streaming updates frequency tables only after assignment.

### 4.3 Fellegi-Sunter

Each available field enters one mutually exclusive agreement bucket and adds:

```text
evidence = log(m_level / u_level)
pair_raw = sum(available field evidence)
confidence = logistic(pair_raw)
```

Smoothing `0.5` prevents zero empirical `u`. Exact email, device, payment, and
IP use empirical value probabilities; fuzzy email and subnet/time levels use
bucket probabilities. Missing levels are omitted. Config validation checks
probabilities and boundaries before linking. Streaming keeps the initial model
fixed.

The selected average-strong rule requires average raw evidence at least `3.0`,
one cross pair at least `3.0`, and no hard constraint. Complete linkage remains
implemented and tested.

### 4.4 Correlation clustering

The corrected baseline compatibility is centered into a signed edge:

```text
edge(a,b) = baseline_score(a,b) - NeutralSimilarity
```

Batch precomputes all edges and greedily merges the legal pair of clusters with
the largest positive average cross-edge. Raw edge sums remain cached and every
accepted merge still increases the signed objective. Streaming uses the same
average for insertion. `verified_distinct` is always a hard reject.

## 5. Experiment protocol

The coarse one-dimensional ranges were declared before running:

| Method | Values | Fixed comparison parameter |
| --- | --- | --- |
| Baseline | `0.35, 0.45, 0.55, 0.65, 0.75` | Strong pair `0.80` |
| Frequency-aware | `0.40, 0.50, 0.60, 0.70` | Strong pair `0.75` |
| Fellegi-Sunter | `-1, 0, 1, 2, 3` | Strong raw evidence `3.0` |
| Correlation | `0.30, 0.40, 0.45, 0.50, 0.60` | Raw-sum vs average support |

There was no multidimensional grid search and no post-truth resolution
refinement. Selection used: more recovered rings, lower BusinessCost, higher
F2, then higher F1. If every setting recovered `0/2`, BusinessCost was treated
as degenerate and F2/F1 were used instead of selecting an extreme low-recall
threshold merely because it contaminated nobody.

```text
BusinessCost = 2000 * missedFraudRings
             +   50 * affectedLegitimateActors
```

### 5.1 Coarse sweep overview

| Method/rule | Parameter results as `value: F2/F1, rings, affected` |
| --- | --- |
| Baseline complete | `.35: .4658/.4615, 0, 21`; `.45: .5000/.5556, 0, 9`; `.55: .3901/.4889, 0, 3`; `.65: .3597/.4651, 0, 1`; `.75: .2941/.4000, 0, 0` |
| Baseline average-strong | All five values: `.1515/.2222, 0, 0`; fixed `0.80` guard was too conservative. |
| Frequency complete | `.40: .4393/.2937, 0, 55`; `.50: .5060/.4722, 0, 29`; `.60: .5629/.6182, 0, 7`; `.70: .3521/.4348, 0, 6` |
| Frequency average-strong | All four values: `.3597/.4651, 0, 1`; fixed `0.75` guard was conservative. |
| FS complete | `-1: .7471/.6667, 1, 24`; `0: .7784/.7324, 1, 15`; `1: .7278/.7419, 1, 10`; `2: .7097/.7458, 1, 8`; `3: .7000/.7778, 1, 2` |
| FS average-strong | `-1: .7099/.6970, 1, 6`; `0: .7372/.7667, 1, 6`; `1: .7372/.7667, 1, 6`; `2: .7468/.7931, 1, 4`; `3: .7566/.8214, 1, 2` |
| Correlation sum/average | Both modes matched on this sample: `.30: .4722/.4048`; `.40: .4777/.4918`; `.45: .4870/.5172`; `.50: .4795/.5600`; `.60: .3929/.5000`; all recovered zero rings. |

Average correlation support did not change the sample output, but it removes a
demonstrated large-cluster correctness bias and is therefore the final default.

## 6. Selected results

There are 100 accounts and 4,950 unordered pairs. Accuracy is shown only as a
diagnostic because true-negative pairs dominate.

| Method | TP / FP / TN / FN | Accuracy | Precision | Recall | F1 | F2 |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Baseline complete `0.45` | 15 / 7 / 4,911 / 17 | 99.5152% | 68.1818% | 46.8750% | 55.5556% | 50.0000% |
| Frequency complete `0.60` | 17 / 6 / 4,912 / 15 | 99.5758% | 73.9130% | 53.1250% | 61.8182% | 56.2914% |
| FS average-strong `3.0` | 23 / 1 / 4,917 / 9 | 99.7980% | 95.8333% | 71.8750% | 82.1429% | 75.6579% |
| Correlation average `0.45` | 15 / 11 / 4,907 / 17 | 99.4343% | 57.6923% | 46.8750% | 51.7241% | 48.7013% |

| Method | Rings recovered | Actor clusters `0001/0002` | Affected legitimate actors | BusinessCost | Clusters / singletons |
| --- | ---: | ---: | ---: | ---: | ---: |
| Baseline | 0 / 2 | 5 / 2 | 9 | 4,450 | 80 / 62 |
| Frequency-aware | 0 / 2 | 5 / 2 | 7 | 4,350 | 79 / 60 |
| Fellegi-Sunter | 1 / 2 | 1 / 2 | 2 | 2,100 | 83 / 70 |
| Correlation | 0 / 2 | 5 / 2 | 10 | 4,500 | 79 / 62 |

Fellegi-Sunter is the strongest sample implementation by the required selection
rule. It is the only method to recover a complete fraud ring and also has the
best precision, recall, F1, F2, and BusinessCost.

## 7. Fraud actor diagnostic

The selected pair thresholds are baseline `0.45`, frequency `0.60`, and FS raw
evidence `3.0`.

| Truth actor/pair | Baseline | Frequency | FS raw evidence |
| --- | ---: | ---: | ---: |
| `0001: 0fb89c / 7145e9` | .1811 | .4188 | 5.0240 |
| `0001: 0fb89c / 7585e8` | .2387 | .4665 | 5.0240 |
| `0001: 0fb89c / d23ce6` | .1120 | .3556 | 5.0240 |
| `0001: 0fb89c / ffa8ec` | .1120 | .3556 | 5.0240 |
| `0001: 7145e9 / 7585e8` | .1753 | .4137 | 5.0240 |
| `0001: 7145e9 / d23ce6` | .3193 | .5270 | 5.0240 |
| `0001: 7145e9 / ffa8ec` | .3193 | .5270 | 5.0240 |
| `0001: 7585e8 / d23ce6` | .2387 | .4666 | 5.0240 |
| `0001: 7585e8 / ffa8ec` | .2153 | .4137 | 5.0240 |
| `0001: d23ce6 / ffa8ec` | .2809 | .4992 | 5.0240 |
| `0002: 0895c0 / 241c89` | .4920 | .6186 | .6875 |
| `0002: 0895c0 / 465bd8` | .0533 | .3423 | .7399 |
| `0002: 0895c0 / 646d6c` | .7803 | .9576 | 12.2207 |
| `0002: 241c89 / 465bd8` | .0720 | .3285 | .6875 |
| `0002: 241c89 / 646d6c` | .5477 | .6733 | 6.7798 |
| `0002: 465bd8 / 646d6c` | .0480 | .3292 | -.1082 |

For `actor_0001`, baseline and frequency have `0/10` passing truth pairs, while
FS has `10/10`; matching evidence is the dominant reason FS alone recovers it.
For `actor_0002`, baseline/frequency have `3/6` passing pairs and FS has `2/6`.
It remains split into two clusters because several pair scores are too low. No
internal fraud-actor pair is blocked by `verified_distinct`; the failure is not
caused by a hard constraint.

## 8. Validation

| Check | Main | Frequency | FS | Correlation |
| --- | --- | --- | --- | --- |
| `gofmt` | pass | pass | pass | pass |
| `go test ./...` | pass | pass | pass | pass |
| `go test -race ./...` | pass | pass | pass | pass |
| Grader build `go build -mod=vendor ...` | pass | pass | pass | pass |
| Batch run twice, byte-identical | pass | pass | pass | pass |
| Every account exactly once | pass | pass | pass | pass |
| `verified_distinct` violations | 0 | 0 | 0 | 0 |
| Streaming JSONL cases | 5/5 | 5/5 | 5/5 | 5/5 |

Streaming cases covered exact plus-tag email, fuzzy email, JSON `null` fields,
same `/24`, and same `/16`. Unit regressions additionally cover blocked best
cluster followed by the next valid cluster, all candidates rejected to a
singleton, every-member constraint checks, deterministic output, malformed
configuration, missing-value semantics, all-pairs batch coverage, and
correlation size bias.

## 9. Final answers

1. No branch has a known Critical or High issue after the audit.
2. The fixed High issues were batch candidate loss, positive missing evidence,
   fuzzy-domain and broad-subnet overconfidence, FS subnet/probability errors,
   and correlation large-cluster size bias.
3. Matching improvements materially improved FS: it now recovers one fraud ring
   with F2 `75.66%`. The other methods improved semantics but still miss both
   sample rings.
4. Average linkage improved FS over complete linkage at the selected raw
   threshold; it did not improve baseline or frequency with their fixed strong
   guards.
5. Normalized correlation support did not change this sample's clusters, but it
   fixes a proven size-bias regression and is safer than raw sum.
6. Fellegi-Sunter performs best on the sample.
7. `main` is the safest conservative take-home submission because it is simplest
   to explain and has no estimated-model calibration risk. The FS branch is the
   best performance submission if its assumptions can be defended clearly.
8. Builds, tests, race tests, deterministic checks, account coverage,
   constraints, batch execution, and streaming checks all pass on every branch.
