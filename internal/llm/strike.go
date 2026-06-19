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

// ShouldRetry returns true if the observation should be retried at the given epoch,
// based on its last rejection epoch and current strike status.
func ShouldRetry(status StrikeStatus, rejectedAtEpoch, currentEpoch int) bool {
	switch status {
	case StrikeOne:
		return currentEpoch-rejectedAtEpoch >= RetryAfterStrike1
	case StrikeTwo:
		return currentEpoch-rejectedAtEpoch >= RetryAfterStrike2
	case StrikeFinal:
		return false
	default:
		return true
	}
}

// IsFinal returns true if no more retries are possible.
func IsFinal(status StrikeStatus) bool {
	return status == StrikeFinal
}
