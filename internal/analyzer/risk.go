package analyzer

import "fmt"

// Thresholds defines the byte thresholds for risk classification.
type Thresholds struct {
	WarningBytes int64
	FailureBytes int64
}

// DefaultThresholds returns the default risk thresholds:
// 50 MB for warning, 100 MB for failure.
func DefaultThresholds() Thresholds {
	return Thresholds{
		WarningBytes: 50 * 1024 * 1024,  // 50 MB
		FailureBytes: 100 * 1024 * 1024, // 100 MB
	}
}

// ClassifyRisk determines the risk level and reasons based on the total
// growth in bytes relative to the given thresholds.
func ClassifyRisk(growthBytes int64, t Thresholds) (riskLevel string, reasons []string) {
	switch {
	case growthBytes >= t.FailureBytes:
		return RiskHigh, []string{
			fmt.Sprintf("Total growth of %s exceeds failure threshold of %s.",
				HumanReadableSize(growthBytes), HumanReadableSize(t.FailureBytes)),
		}
	case growthBytes >= t.WarningBytes:
		return RiskMedium, []string{
			fmt.Sprintf("Total growth of %s exceeds warning threshold of %s.",
				HumanReadableSize(growthBytes), HumanReadableSize(t.WarningBytes)),
		}
	default:
		return RiskLow, nil
	}
}

// RecommendAction returns a human-readable recommendation string based on
// the risk level and file characteristics.
func RecommendAction(riskLevel string, isBinary bool, largestFile *FileImpact) string {
	switch riskLevel {
	case RiskHigh:
		if isBinary {
			return "Use Git LFS for this file, or store it in external artifact storage (S3, GCS, etc.) instead of committing it directly."
		}
		return "This is an unusually large text-based file. Consider whether it should be generated at build time instead of committed."
	case RiskMedium:
		return "Monitor this — consider Git LFS if this file will be modified frequently."
	default:
		return ""
	}
}
