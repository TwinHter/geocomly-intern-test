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

func TestDefaultConfig(t *testing.T) {
	config := similarity.DefaultConfig()
	weightSum := config.EmailWeight + config.DeviceWeight + config.PaymentWeight +
		config.IPWeight + config.TimeWeight
	if math.Abs(weightSum-1) > 1e-9 {
		t.Fatalf("weights sum to %.4f, want 1", weightSum)
	}
	if config.MergeThreshold <= 0 || config.MergeThreshold > 1 {
		t.Fatalf("invalid merge threshold %.4f", config.MergeThreshold)
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
		{name: "different local", a: "alice@gmail.com", b: "robert@gmail.com", min: 0.10, max: 0.40},
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

func TestIPSimilarity(t *testing.T) {
	scorer := similarity.New(similarity.DefaultConfig())
	tests := []struct {
		name string
		a    string
		b    string
		want float64
	}{
		{name: "IPv4 exact", a: "203.0.113.4", b: "203.0.113.4", want: 1},
		{name: "IPv4 /24", a: "203.0.113.4", b: "203.0.113.90", want: 0.85},
		{name: "IPv4 /16", a: "203.0.113.4", b: "203.0.9.1", want: 0.55},
		{name: "IPv4 different", a: "203.0.113.4", b: "198.51.100.1", want: 0},
		{name: "IPv6 /64", a: "2001:db8:1:2::1", b: "2001:db8:1:2::99", want: 0.85},
		{name: "IPv6 /48", a: "2001:db8:1:2::1", b: "2001:db8:1:9::1", want: 0.55},
		{name: "IPv4 mapped", a: "::ffff:203.0.113.4", b: "203.0.113.4", want: 1},
		{name: "missing", a: "", b: "203.0.113.4", want: 0.20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scorer.IPSimilarity(tt.a, tt.b); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("IPSimilarity() = %.4f, want %.4f", got, tt.want)
			}
		})
	}
}

func TestScoreMissingValuesAndBounds(t *testing.T) {
	config := similarity.DefaultConfig()
	scorer := similarity.New(config)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := model.Account{Email: "same@example.com", CreatedAt: created}
	want := config.EmailWeight + config.DeviceWeight*config.MissingValueScore +
		config.PaymentWeight*config.MissingValueScore + config.IPWeight*config.MissingValueScore + config.TimeWeight
	if got := scorer.Score(a, a); math.Abs(got-want) > 1e-9 {
		t.Fatalf("Score() = %.4f, want %.4f", got, want)
	}

	config.EmailWeight = 1
	config.DeviceWeight = 0
	config.PaymentWeight = 0
	config.IPWeight = 0
	config.TimeWeight = 0
	if got := similarity.New(config).Score(model.Account{}, model.Account{}); got != config.MissingValueScore {
		t.Fatalf("missing email score = %.4f, want %.4f", got, config.MissingValueScore)
	}

	exact := model.Account{
		Email:              "a@example.com",
		DeviceID:           "d",
		PaymentFingerprint: "p",
		IP:                 "192.0.2.1",
		CreatedAt:          created,
	}
	if got := scorer.Score(exact, exact); got != 1 {
		t.Fatalf("Score(exact, exact) = %.4f, want 1", got)
	}
}

func TestCompleteLinkageDoesNotUseNaiveTransitivity(t *testing.T) {
	config := similarity.DefaultConfig()
	config.EmailWeight = 1
	config.DeviceWeight = 0
	config.PaymentWeight = 0
	config.IPWeight = 0
	config.TimeWeight = 0
	config.MergeThreshold = 0.70
	accounts := []model.Account{
		testAccount("a", "aaaa@example.com"),
		testAccount("b", "aaab@example.com"),
		testAccount("c", "aabb@example.com"),
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
	if len(batch.Clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(batch.Clusters))
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
	if assignment.AccountID != "new" || assignment.ClusterID != "c1" {
		t.Fatalf("unexpected assignment: %+v", assignment)
	}
}
