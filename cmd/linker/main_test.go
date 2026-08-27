package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"accountlinker/internal/linker"
	"accountlinker/internal/model"
	"accountlinker/internal/similarity"
)

var testTime = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func testAccount(id, email string) model.Account {
	return model.Account{
		AccountID: id,
		Email:     email,
		DeviceID:  "device",
		IP:        "192.0.2.10",
		CreatedAt: testTime,
	}
}

func verifiedDistinct(a, b string) model.Constraint {
	return model.Constraint{AccountA: a, AccountB: b, Relation: "verified_distinct"}
}

func permissiveConfig() similarity.Config {
	config := similarity.DefaultConfig()
	config.LinkageRule = similarity.CompleteLinkage
	config.LinkEvidenceThreshold = -100
	return config
}

func TestDefaultConfig(t *testing.T) {
	config := similarity.DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("invalid default config: %v", err)
	}
	if config.Smoothing <= 0 || config.MaxEstimationPairs <= 0 {
		t.Fatalf("invalid estimation config: %+v", config)
	}
	if config.EmailMediumThreshold >= config.EmailVeryHighThreshold {
		t.Fatalf("email thresholds are not ordered: %+v", config)
	}
	if config.LinkageRule != similarity.AverageStrongLinkage {
		t.Fatalf("default linkage = %q, want average-strong", config.LinkageRule)
	}
}

func TestInvalidProbabilityConfigIsRejected(t *testing.T) {
	config := similarity.DefaultConfig()
	config.IPM[2] = 0
	_, err := linker.Batch([]model.Account{testAccount("a", "a@example.com")}, nil, config)
	if err == nil {
		t.Fatal("Batch accepted an invalid zero m probability")
	}
}

func TestEmailSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		min  float64
		max  float64
	}{
		{name: "normalized exact", a: " User@Example.com ", b: "user@example.com", min: 1, max: 1},
		{name: "plus alias", a: "user+checkout@example.com", b: "user@example.com", min: 1, max: 1},
		{name: "small typo", a: "john.doe@gmail.com", b: "j0hn.doe@gmail.com", min: 0.85, max: 0.95},
		{name: "different local", a: "alice@gmail.com", b: "robert@gmail.com", min: 0, max: 0.20},
		{name: "missing is not exact", a: "", b: "", min: 0, max: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := similarity.EmailSimilarity(tt.a, tt.b)
			if got < tt.min || got > tt.max {
				t.Fatalf("EmailSimilarity() = %.4f, want [%.2f, %.2f]", got, tt.min, tt.max)
			}
		})
	}
}

func TestScoresStayFiniteWithSmoothing(t *testing.T) {
	config := similarity.DefaultConfig()
	config.DeviceM = [2]float64{}
	accounts := []model.Account{{DeviceID: "a"}, {DeviceID: "b"}}
	evidence := similarity.New(config, accounts).PairEvidence(accounts[0], accounts[1])
	if math.IsNaN(evidence.Raw) || math.IsInf(evidence.Raw, 0) ||
		math.IsNaN(evidence.Confidence) || math.IsInf(evidence.Confidence, 0) {
		t.Fatalf("non-finite evidence with smoothing: %+v", evidence)
	}
}

func TestLargerMLikelihoodRatioProducesStrongerEvidence(t *testing.T) {
	accounts := []model.Account{{DeviceID: "rare"}, {DeviceID: "rare"}, {DeviceID: "other"}}
	lowConfig := similarity.DefaultConfig()
	lowConfig.DeviceM[0] = 0.40
	highConfig := lowConfig
	highConfig.DeviceM[0] = 0.80
	low := similarity.New(lowConfig, accounts).PairEvidence(accounts[0], accounts[1]).Device
	high := similarity.New(highConfig, accounts).PairEvidence(accounts[0], accounts[1]).Device
	if high <= low {
		t.Fatalf("higher m/u evidence %.4f must exceed %.4f", high, low)
	}
}

func TestRareExactSignalsAreStrongerThanCommonSignals(t *testing.T) {
	accounts := []model.Account{
		{DeviceID: "rare-device", PaymentFingerprint: "rare-payment"},
		{DeviceID: "rare-device", PaymentFingerprint: "rare-payment"},
		{DeviceID: "common", PaymentFingerprint: "common"},
		{DeviceID: "common", PaymentFingerprint: "common"},
		{DeviceID: "common", PaymentFingerprint: "common"},
		{DeviceID: "common", PaymentFingerprint: "common"},
	}
	scorer := similarity.New(similarity.DefaultConfig(), accounts)
	rare := scorer.PairEvidence(accounts[0], accounts[1])
	common := scorer.PairEvidence(accounts[2], accounts[3])
	if rare.Device <= 0 || rare.Payment <= 0 {
		t.Fatalf("rare exact signals are not positive: %+v", rare)
	}
	if rare.Device <= common.Device || rare.Payment <= common.Payment {
		t.Fatalf("rare=%+v must be stronger than common=%+v", rare, common)
	}
}

func TestDisagreementIsNegativeAndMissingIsNeutral(t *testing.T) {
	accounts := []model.Account{
		{DeviceID: "a", PaymentFingerprint: "p1"},
		{DeviceID: "b", PaymentFingerprint: "p2"},
		{DeviceID: "c", PaymentFingerprint: "p3"},
	}
	scorer := similarity.New(similarity.DefaultConfig(), accounts)
	disagreement := scorer.PairEvidence(accounts[0], accounts[1])
	if disagreement.Device >= 0 || disagreement.Payment >= 0 {
		t.Fatalf("disagreement is not negative: %+v", disagreement)
	}
	missing := scorer.PairEvidence(model.Account{}, model.Account{})
	if missing.Raw != 0 || missing.Confidence != 0.5 {
		t.Fatalf("missing signals are not neutral: %+v", missing)
	}
}

func TestBatchScoresAllPairsButStreamingUsesCandidates(t *testing.T) {
	config := similarity.DefaultConfig()
	config.LinkageRule = similarity.CompleteLinkage
	config.EmailVeryHighThreshold = 0.75
	config.EmailM = [4]float64{0.05, 0.80, 0.10, 0.05}
	config.LinkEvidenceThreshold = 0.20
	accounts := []model.Account{
		{AccountID: "a", Email: "abcde@example.com", CreatedAt: testTime},
		{AccountID: "b", Email: "abxde@example.com", CreatedAt: testTime},
	}
	state, err := linker.Batch(accounts, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(state.Output().Clusters); got != 1 {
		t.Fatalf("all-pairs batch produced %d clusters, want 1", got)
	}

	streamState, err := linker.Batch(accounts[:1], nil, config)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := streamState.Add(accounts[1])
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ClusterID == "c1" {
		t.Fatal("streaming unexpectedly found a pair with no shared block")
	}
}

func TestCompleteLinkageDoesNotUseNaiveTransitivity(t *testing.T) {
	config := similarity.DefaultConfig()
	config.LinkageRule = similarity.CompleteLinkage
	config.EmailM = [4]float64{0.05, 0.80, 0.10, 0.05}
	accounts := []model.Account{
		{AccountID: "a", Email: "aaaaaaaaaa@example.com", CreatedAt: testTime},
		{AccountID: "b", Email: "aaaaaaaaba@example.com", CreatedAt: testTime},
		{AccountID: "c", Email: "aaaaaaabba@example.com", CreatedAt: testTime},
	}
	scorer := similarity.New(config, accounts)
	adjacent := math.Min(scorer.RawScore(accounts[0], accounts[1]), scorer.RawScore(accounts[1], accounts[2]))
	endpoint := scorer.RawScore(accounts[0], accounts[2])
	if adjacent <= endpoint {
		t.Fatalf("test setup has adjacent=%f endpoint=%f", adjacent, endpoint)
	}
	config.LinkEvidenceThreshold = (adjacent + endpoint) / 2
	state, err := linker.Batch(accounts, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	output := state.Output()
	if len(output.Clusters) != 2 {
		t.Fatalf("got %d clusters, want 2: %+v", len(output.Clusters), output.Clusters)
	}
	for _, cluster := range output.Clusters {
		if len(cluster.AccountIDs) == 3 {
			t.Fatalf("incompatible endpoints merged transitively: %+v", cluster)
		}
	}
}

func TestAverageStrongLinkageUsesRawEvidenceGuard(t *testing.T) {
	config := similarity.DefaultConfig()
	config.EmailM = [4]float64{0.05, 0.80, 0.10, 0.05}
	accounts := []model.Account{
		{AccountID: "a", Email: "aaaaaaaaaa@example.com", CreatedAt: testTime},
		{AccountID: "b", Email: "aaaaaaaaba@example.com", CreatedAt: testTime},
		{AccountID: "c", Email: "aaaaaaabba@example.com", CreatedAt: testTime},
	}
	scorer := similarity.New(config, accounts)
	adjacent := math.Min(scorer.RawScore(accounts[0], accounts[1]), scorer.RawScore(accounts[1], accounts[2]))
	endpoint := scorer.RawScore(accounts[0], accounts[2])
	config.LinkEvidenceThreshold = (adjacent + endpoint) / 2
	config.StrongEvidenceThreshold = adjacent
	config.LinkageRule = similarity.AverageStrongLinkage
	state, err := linker.Batch(accounts, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(state.Output().Clusters); got != 1 {
		t.Fatalf("average-strong linkage produced %d clusters, want 1", got)
	}
}

func TestVerifiedDistinctOverridesIdenticalSignals(t *testing.T) {
	accounts := []model.Account{
		testAccount("a", "same@example.com"),
		testAccount("b", "same@example.com"),
	}
	state, err := linker.Batch(accounts, []model.Constraint{verifiedDistinct("a", "b")}, permissiveConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(state.Output().Clusters); got != 2 {
		t.Fatalf("got %d clusters, want 2", got)
	}
}

func TestStreamingRejectsConflictingBestClusterAndTriesNext(t *testing.T) {
	accounts := []model.Account{
		testAccount("a", "same@example.com"),
		testAccount("b", "same@example.com"),
	}
	constraints := []model.Constraint{
		verifiedDistinct("a", "b"),
		verifiedDistinct("new", "a"),
	}
	state, err := linker.Batch(accounts, constraints, permissiveConfig())
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := state.Add(testAccount("new", "same@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ClusterID != "c2" {
		t.Fatalf("new account joined %s, want c2", assignment.ClusterID)
	}
}

func TestStreamingCreatesSingletonAfterAllCandidatesRejected(t *testing.T) {
	accounts := []model.Account{
		testAccount("a", "same@example.com"),
		testAccount("b", "same@example.com"),
		testAccount("c", "same@example.com"),
	}
	constraints := []model.Constraint{
		verifiedDistinct("a", "b"),
		verifiedDistinct("a", "c"),
		verifiedDistinct("b", "c"),
		verifiedDistinct("new", "a"),
		verifiedDistinct("new", "b"),
		verifiedDistinct("new", "c"),
	}
	state, err := linker.Batch(accounts, constraints, permissiveConfig())
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := state.Add(testAccount("new", "same@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ClusterID != "c4" || assignment.Confidence != 1 {
		t.Fatalf("assignment = %+v, want singleton c4 with confidence 1", assignment)
	}
}

func TestStreamingChecksEveryClusterMember(t *testing.T) {
	a := testAccount("a", "same@example.com")
	b := testAccount("b", "sane@example.com")
	state, err := linker.Batch(
		[]model.Account{a, b},
		[]model.Constraint{verifiedDistinct("new", "b")},
		permissiveConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Output().Clusters) != 1 {
		t.Fatal("test setup: a and b must begin in one cluster")
	}
	assignment, err := state.Add(testAccount("new", "same@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ClusterID != "c2" {
		t.Fatalf("assignment = %+v, want singleton c2", assignment)
	}
}

func TestBatchIsDeterministicAndAssignsEveryAccountOnce(t *testing.T) {
	accounts := []model.Account{
		testAccount("z", "z@example.com"),
		testAccount("a", "same@example.com"),
		testAccount("b", "same+alt@example.com"),
	}
	first, err := linker.Batch(accounts, nil, similarity.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].AccountID < accounts[j].AccountID })
	second, err := linker.Batch(accounts, nil, similarity.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Output(), second.Output()) {
		t.Fatalf("outputs differ: first=%+v second=%+v", first.Output(), second.Output())
	}

	seen := make(map[string]int)
	for _, cluster := range first.Output().Clusters {
		for _, id := range cluster.AccountIDs {
			seen[id]++
		}
	}
	for _, input := range accounts {
		if seen[input.AccountID] != 1 {
			t.Fatalf("account %s appeared %d times", input.AccountID, seen[input.AccountID])
		}
	}
}

func TestLinkAndStreamCommands(t *testing.T) {
	temp := t.TempDir()
	accountsPath := filepath.Join(temp, "accounts.jsonl")
	constraintsPath := filepath.Join(temp, "constraints.jsonl")
	outputPath := filepath.Join(temp, "clusters.json")
	accounts := "{\"account_id\":\"a\",\"email\":\"same@example.com\",\"device_id\":\"d\",\"ip\":\"192.0.2.1\",\"payment_fingerprint\":null,\"created_at\":\"2026-06-01T12:00:00Z\"}\n" +
		"{\"account_id\":\"b\",\"email\":\"same+alt@example.com\",\"device_id\":\"d\",\"ip\":\"192.0.2.2\",\"payment_fingerprint\":null,\"created_at\":\"2026-06-01T13:00:00Z\"}\n"
	if err := os.WriteFile(accountsPath, []byte(accounts), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(constraintsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"link", "--accounts", accountsPath, "--constraints", constraintsPath, "--output", outputPath}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var batch model.BatchOutput
	if err := json.Unmarshal(encoded, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Clusters) == 0 {
		t.Fatal("batch command returned no clusters")
	}

	streamInput := "{\"account_id\":\"new\",\"email\":\"same@example.com\",\"device_id\":\"d\",\"ip\":\"192.0.2.3\",\"payment_fingerprint\":null,\"created_at\":\"2026-06-01T14:00:00Z\"}\n"
	var stdout bytes.Buffer
	if err := run([]string{"stream", "--accounts", accountsPath, "--constraints", constraintsPath}, strings.NewReader(streamInput), &stdout); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d output lines, want 1", len(lines))
	}
	var assignment model.StreamOutput
	if err := json.Unmarshal([]byte(lines[0]), &assignment); err != nil {
		t.Fatal(err)
	}
	if assignment.AccountID != "new" || assignment.ClusterID == "" ||
		assignment.Confidence < 0 || assignment.Confidence > 1 {
		t.Fatalf("unexpected assignment: %+v", assignment)
	}
}
