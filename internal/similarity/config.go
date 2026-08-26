package similarity

import "time"

// Config contains every tunable scoring and blocking parameter.
type Config struct {
	Smoothing              float64
	LinkEvidenceThreshold  float64
	SingletonConfidence    float64
	MaxEstimationPairs     int
	EmailVeryHighThreshold float64
	EmailMediumThreshold   float64
	TimeVeryClose          time.Duration
	TimeClose              time.Duration
	TimeModerate           time.Duration

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
		Smoothing:              0.5,
		LinkEvidenceThreshold:  1.0,
		SingletonConfidence:    1,
		MaxEstimationPairs:     100_000,
		EmailVeryHighThreshold: 0.85,
		EmailMediumThreshold:   0.60,
		TimeVeryClose:          24 * time.Hour,
		TimeClose:              7 * 24 * time.Hour,
		TimeModerate:           30 * 24 * time.Hour,

		EmailM:   [4]float64{0.35, 0.30, 0.20, 0.15},
		DeviceM:  [2]float64{0.55, 0.45},
		PaymentM: [2]float64{0.60, 0.40},
		IPM:      [4]float64{0.30, 0.25, 0.20, 0.25},
		TimeM:    [4]float64{0.15, 0.30, 0.30, 0.25},

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
