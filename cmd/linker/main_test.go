package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestDefaultConfig(t *testing.T) {
	config := similarity.DefaultConfig()
	if config.Smoothing <= 0 || config.EvidenceScale <= 0 {
		t.Fatalf("invalid evidence config: %+v", config)
	}
	if config.MergeThreshold <= 0 || config.MergeThreshold > 1 {
		t.Fatalf("invalid merge threshold %.4f", config.MergeThreshold)
	}
	if config.LinkageRule != similarity.CompleteLinkage {
		t.Fatalf("default linkage = %q, want complete", config.LinkageRule)
	}
	if config.StrongPairThreshold < config.MergeThreshold || config.StrongPairThreshold > 1 {
		t.Fatalf("invalid strong-pair threshold %.4f", config.StrongPairThreshold)
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

func TestRareExactValueProvidesMoreEvidence(t *testing.T) {
	accounts := []model.Account{
		{DeviceID: "rare"}, {DeviceID: "rare"},
		{DeviceID: "common"}, {DeviceID: "common"}, {DeviceID: "common"}, {DeviceID: "common"},
	}
	scorer := similarity.New(similarity.DefaultConfig(), accounts)
	rare := scorer.PairEvidence(accounts[0], accounts[1]).Device
	common := scorer.PairEvidence(accounts[2], accounts[3]).Device
	wantRare := math.Log(8.0 / 3.0)
	if math.Abs(rare-wantRare) > 1e-9 {
		t.Fatalf("rare evidence = %.4f, want rarity %.4f", rare, wantRare)
	}
	if rare <= common {
		t.Fatalf("rare evidence %.4f must exceed common evidence %.4f", rare, common)
	}
}

func TestCommonIPDoesNotDominateScore(t *testing.T) {
	accounts := make([]model.Account, 20)
	for i := range accounts {
		accounts[i].IP = fmt.Sprintf("203.0.113.%d", i+1)
	}
	scorer := similarity.New(similarity.DefaultConfig(), accounts)
	if score := scorer.Score(accounts[0], accounts[1]); score >= similarity.DefaultConfig().MergeThreshold {
		t.Fatalf("common /24 score %.4f reached merge threshold", score)
	}
}

func TestIPEvidenceHierarchy(t *testing.T) {
	accounts := []model.Account{
		{IP: "203.0.113.4"}, {IP: "203.0.113.4"},
		{IP: "203.0.113.90"}, {IP: "203.0.113.91"},
		{IP: "203.0.9.1"}, {IP: "203.0.8.1"},
	}
	scorer := similarity.New(similarity.DefaultConfig(), accounts)
	exact := scorer.PairEvidence(accounts[0], accounts[1]).IP
	high := scorer.PairEvidence(accounts[0], accounts[2]).IP
	mid := scorer.PairEvidence(accounts[0], accounts[4]).IP
	different := scorer.PairEvidence(accounts[0], model.Account{IP: "198.51.100.1"}).IP
	if !(exact > high && high > mid && mid > different) {
		t.Fatalf("IP evidence hierarchy exact=%f /24=%f /16=%f different=%f", exact, high, mid, different)
	}
}

func TestScoreMissingValuesAndBounds(t *testing.T) {
	config := similarity.DefaultConfig()
	if got := similarity.New(config, nil).Score(model.Account{}, model.Account{}); got != 0 {
		t.Fatalf("missing signals score = %.4f, want 0", got)
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	exact := model.Account{
		Email:              "a@example.com",
		DeviceID:           "d",
		PaymentFingerprint: "p",
		IP:                 "192.0.2.1",
		CreatedAt:          created,
	}
	scorer := similarity.New(config, []model.Account{exact, exact})
	evidence := scorer.PairEvidence(exact, exact)
	if evidence.Raw <= 0 || evidence.Score <= 0 || evidence.Score >= 1 {
		t.Fatalf("unexpected normalized evidence: %+v", evidence)
	}
	before := evidence.Device
	scorer.Add(exact)
	after := scorer.PairEvidence(exact, exact).Device
	if after >= before {
		t.Fatalf("device evidence did not weaken as value became more common: before=%f after=%f", before, after)
	}
	if first, second := scorer.Score(exact, exact), scorer.Score(exact, exact); first != second {
		t.Fatalf("scoring is not deterministic: first=%f second=%f", first, second)
	}
}

func TestBatchScoresAllPairsButStreamingUsesCandidates(t *testing.T) {
	config := similarity.DefaultConfig()
	config.EvidenceScale = 0.20
	config.MergeThreshold = 0.80
	config.StrongPairThreshold = 0.85
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

func TestStreamingUpdatesFrequencyState(t *testing.T) {
	state, err := linker.Batch([]model.Account{testAccount("initial", "same@example.com")}, nil, similarity.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.Add(testAccount("new-1", "same@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.Add(testAccount("new-2", "same@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ClusterID != "c1" || second.ClusterID != "c1" {
		t.Fatalf("streamed assignments left initial cluster: first=%+v second=%+v", first, second)
	}
	if second.Confidence >= first.Confidence {
		t.Fatalf("frequency update did not weaken repeated evidence: first=%f second=%f", first.Confidence, second.Confidence)
	}
}

func TestCompleteLinkageDoesNotUseNaiveTransitivity(t *testing.T) {
	config := similarity.DefaultConfig()
	config.EvidenceScale = 0.5
	config.MergeThreshold = 0.68
	config.MinEmailNGramMatches = 1
	accounts := []model.Account{
		{AccountID: "a", Email: "aaaaa@example.com", CreatedAt: testTime},
		{AccountID: "b", Email: "aaaab@example.com", CreatedAt: testTime},
		{AccountID: "c", Email: "aaabb@example.com", CreatedAt: testTime},
	}
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

func TestAverageStrongLinkageCanAcceptModerateAverage(t *testing.T) {
	config := similarity.DefaultConfig()
	config.EvidenceScale = 0.50
	config.MergeThreshold = 0.65
	config.StrongPairThreshold = 0.70
	accounts := []model.Account{
		{AccountID: "a", Email: "aaaaa@example.com", CreatedAt: testTime},
		{AccountID: "b", Email: "aaaab@example.com", CreatedAt: testTime},
		{AccountID: "c", Email: "aaabb@example.com", CreatedAt: testTime},
	}
	complete, err := linker.Batch(accounts, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(complete.Output().Clusters); got != 2 {
		t.Fatalf("complete linkage produced %d clusters, want 2", got)
	}

	config.LinkageRule = similarity.AverageStrongLinkage
	average, err := linker.Batch(accounts, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(average.Output().Clusters); got != 1 {
		t.Fatalf("average-strong linkage produced %d clusters, want 1", got)
	}
}

func TestVerifiedDistinctOverridesIdenticalSignals(t *testing.T) {
	accounts := []model.Account{
		testAccount("a", "same@example.com"),
		testAccount("b", "same@example.com"),
	}
	state, err := linker.Batch(accounts, []model.Constraint{verifiedDistinct("a", "b")}, similarity.DefaultConfig())
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
	state, err := linker.Batch(accounts, constraints, similarity.DefaultConfig())
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
	state, err := linker.Batch(accounts, constraints, similarity.DefaultConfig())
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
		similarity.DefaultConfig(),
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
	accounts := "{\"account_id\":\"a\",\"email\":\"same@example.com\",\"device_id\":\"d\",\"ip\":\"192.0.2.1\",\"payment_fingerprint\":\"p\",\"created_at\":\"2026-06-01T12:00:00Z\"}\n" +
		"{\"account_id\":\"b\",\"email\":\"same+alt@example.com\",\"device_id\":\"d\",\"ip\":\"192.0.2.2\",\"payment_fingerprint\":\"p\",\"created_at\":\"2026-06-01T13:00:00Z\"}\n"
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
	if len(batch.Clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(batch.Clusters))
	}

	streamInput := "{\"account_id\":\"new\",\"email\":\"same@example.com\",\"device_id\":\"d\",\"ip\":\"192.0.2.3\",\"payment_fingerprint\":\"p\",\"created_at\":\"2026-06-01T14:00:00Z\"}\n"
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
	if assignment.AccountID != "new" || assignment.ClusterID != "c1" {
		t.Fatalf("unexpected assignment: %+v", assignment)
	}
}
