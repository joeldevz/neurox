package llm

// StrikeStatus represents the current strike state.
type StrikeStatus string

const (
	StrikeNone     StrikeStatus = ""
	StrikeOne      StrikeStatus = "rejected"
	StrikeTwo      StrikeStatus = "rejected-2"
	StrikeFinal    StrikeStatus = "rejected-final"
)

const (
	RetryAfterStrike1 = 48  // epochs before retrying after first rejection
	RetryAfterStrike2 = 144 // epochs before retrying after second rejection
)

// NextStrike returns the next strike status after a rejection.
func NextStrike(current StrikeStatus) StrikeStatus {
	switch current {
	case StrikeNone:
		return StrikeOne
	case StrikeOne:
		return StrikeTwo
	case StrikeTwo:
		return StrikeFinal
	default:
		return StrikeFinal
	}
}
