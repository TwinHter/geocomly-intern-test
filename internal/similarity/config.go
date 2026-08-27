package similarity

import "time"

type LinkageRule string

const (
	CompleteLinkage      LinkageRule = "complete"
	AverageStrongLinkage LinkageRule = "average-strong"
)

// Config contains every tunable scoring, blocking, and confidence parameter.
type Config struct {
	EmailWeight   float64
	DeviceWeight  float64
	PaymentWeight float64
	IPWeight      float64
	TimeWeight    float64

	MergeThreshold      float64
	StrongPairThreshold float64
	LinkageRule         LinkageRule
	SingletonConfidence float64

	EmailExactDomainBonus float64
	FuzzyEmailMax         float64
	EmailNGramSize        int
	MinEmailNGramMatches  int
	ShortEmailRuneLength  int

	IPv4HighPrefixBits int
	IPv4MidPrefixBits  int
	IPv6HighPrefixBits int
	IPv6MidPrefixBits  int
	IPHighScore        float64
	IPMidScore         float64

	TimeVeryClose     time.Duration
	TimeClose         time.Duration
	TimeModerate      time.Duration
	TimeFar           time.Duration
	TimeCloseScore    float64
	TimeModerateScore float64
	TimeFarScore      float64

	MaxBlockSize  int
	MaxCandidates int
}

func DefaultConfig() Config {
	return Config{
		EmailWeight:   0.40,
		DeviceWeight:  0.25,
		PaymentWeight: 0.25,
		IPWeight:      0.08,
		TimeWeight:    0.02,

		MergeThreshold:      0.45,
		StrongPairThreshold: 0.80,
		LinkageRule:         CompleteLinkage,
		SingletonConfidence: 1.0,

		EmailExactDomainBonus: 0.05,
		FuzzyEmailMax:         0.85,
		EmailNGramSize:        3,
		MinEmailNGramMatches:  2,
		ShortEmailRuneLength:  4,

		IPv4HighPrefixBits: 24,
		IPv4MidPrefixBits:  16,
		IPv6HighPrefixBits: 64,
		IPv6MidPrefixBits:  48,
		IPHighScore:        0.45,
		IPMidScore:         0.15,

		TimeVeryClose:     time.Hour,
		TimeClose:         24 * time.Hour,
		TimeModerate:      7 * 24 * time.Hour,
		TimeFar:           30 * 24 * time.Hour,
		TimeCloseScore:    0.80,
		TimeModerateScore: 0.50,
		TimeFarScore:      0.20,

		MaxBlockSize:  256,
		MaxCandidates: 4096,
	}
}
