package similarity

import (
	"fmt"
	"math"
	"time"
)

type LinkageRule string

const (
	CompleteLinkage      LinkageRule = "complete"
	AverageStrongLinkage LinkageRule = "average-strong"
)

// Config contains every tunable scoring and blocking parameter.
type Config struct {
	Smoothing               float64
	LinkEvidenceThreshold   float64
	StrongEvidenceThreshold float64
	LinkageRule             LinkageRule
	SingletonConfidence     float64
	MaxEstimationPairs      int
	EmailVeryHighThreshold  float64
	EmailMediumThreshold    float64
	TimeVeryClose           time.Duration
	TimeClose               time.Duration
	TimeModerate            time.Duration

	// M probabilities are ordered from strongest agreement to disagreement.
	EmailM   [4]float64 // exact, very high, medium, low
	DeviceM  [2]float64 // exact, different
	PaymentM [2]float64 // exact, different
	IPM      [4]float64 // exact, same high subnet, same mid subnet, different
	TimeM    [4]float64 // very close, close, moderate, far

	IPv4HighPrefixBits int
	IPv4MidPrefixBits  int
	IPv6HighPrefixBits int
	IPv6MidPrefixBits  int

	EmailNGramSize       int
	MinEmailNGramMatches int
	ShortEmailRuneLength int
	MaxBlockSize         int
	MaxCandidates        int
}

func DefaultConfig() Config {
	return Config{
		Smoothing:               0.5,
		LinkEvidenceThreshold:   3.0,
		StrongEvidenceThreshold: 3.0,
		LinkageRule:             AverageStrongLinkage,
		SingletonConfidence:     1,
		MaxEstimationPairs:      100_000,
		EmailVeryHighThreshold:  0.85,
		EmailMediumThreshold:    0.60,
		TimeVeryClose:           24 * time.Hour,
		TimeClose:               7 * 24 * time.Hour,
		TimeModerate:            30 * 24 * time.Hour,

		EmailM:   [4]float64{0.35, 0.30, 0.20, 0.15},
		DeviceM:  [2]float64{0.55, 0.45},
		PaymentM: [2]float64{0.60, 0.40},
		IPM:      [4]float64{0.45, 0.20, 0.05, 0.30},
		TimeM:    [4]float64{0.40, 0.30, 0.20, 0.10},

		IPv4HighPrefixBits: 24,
		IPv4MidPrefixBits:  16,
		IPv6HighPrefixBits: 64,
		IPv6MidPrefixBits:  48,

		EmailNGramSize:       3,
		MinEmailNGramMatches: 2,
		ShortEmailRuneLength: 4,
		MaxBlockSize:         256,
		MaxCandidates:        4096,
	}
}

func (c Config) Validate() error {
	if c.Smoothing <= 0 {
		return fmt.Errorf("smoothing must be positive")
	}
	if c.MaxEstimationPairs <= 0 {
		return fmt.Errorf("max estimation pairs must be positive")
	}
	if c.EmailMediumThreshold < 0 || c.EmailMediumThreshold >= c.EmailVeryHighThreshold ||
		c.EmailVeryHighThreshold > 1 {
		return fmt.Errorf("email agreement thresholds are invalid")
	}
	if c.LinkageRule != CompleteLinkage && c.LinkageRule != AverageStrongLinkage {
		return fmt.Errorf("unsupported linkage rule %q", c.LinkageRule)
	}
	if c.LinkageRule == AverageStrongLinkage && c.StrongEvidenceThreshold < c.LinkEvidenceThreshold {
		return fmt.Errorf("strong evidence threshold must be at least the link threshold")
	}
	if !(c.TimeVeryClose > 0 && c.TimeVeryClose < c.TimeClose && c.TimeClose < c.TimeModerate) {
		return fmt.Errorf("time agreement boundaries are invalid")
	}
	for name, probabilities := range map[string][]float64{
		"email": c.EmailM[:], "device": c.DeviceM[:], "payment": c.PaymentM[:],
		"ip": c.IPM[:], "time": c.TimeM[:],
	} {
		total := 0.0
		for _, probability := range probabilities {
			if probability <= 0 || probability >= 1 {
				return fmt.Errorf("%s m probabilities must be in (0,1)", name)
			}
			total += probability
		}
		if math.Abs(total-1) > 1e-9 {
			return fmt.Errorf("%s m probabilities sum to %.6f, want 1", name, total)
		}
	}
	return nil
}
