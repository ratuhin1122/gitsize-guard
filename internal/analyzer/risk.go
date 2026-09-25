package analyzer

import (
	"fmt"
	"path"
)

// Thresholds defines the byte thresholds for risk classification.
type Thresholds struct {
	WarningBytes int64
	FailureBytes int64
}

// MaxThresholdMB caps thresholds so the MB-to-byte conversion cannot overflow.
const MaxThresholdMB = 1 << 20 // 1 TiB

// DefaultThresholds returns the default risk thresholds:
// 50 MB for warning, 100 MB for failure (GitHub rejects files over 100 MB).
func DefaultThresholds() Thresholds {
	return Thresholds{
		WarningBytes: 50 * 1024 * 1024,  // 50 MB
		FailureBytes: 100 * 1024 * 1024, // 100 MB
	}
}

// ThresholdsFromMB converts megabyte limits to Thresholds, rejecting values
// that are negative, inverted, or large enough to overflow.
func ThresholdsFromMB(warningMB, failureMB int) (Thresholds, error) {
	switch {
	case warningMB < 0 || failureMB < 0:
		return Thresholds{}, fmt.Errorf("thresholds must not be negative (warning %d MB, failure %d MB)", warningMB, failureMB)
	case warningMB > MaxThresholdMB || failureMB > MaxThresholdMB:
		return Thresholds{}, fmt.Errorf("thresholds must be at most %d MB", MaxThresholdMB)
	case warningMB > failureMB:
		return Thresholds{}, fmt.Errorf("warning threshold (%d MB) exceeds failure threshold (%d MB)", warningMB, failureMB)
	}
	return Thresholds{
		WarningBytes: int64(warningMB) * 1024 * 1024,
		FailureBytes: int64(failureMB) * 1024 * 1024,
	}, nil
}

// ClassifyRisk determines the risk level and reasons based on the total
// growth in bytes relative to the given thresholds. No growth is never risky.
func ClassifyRisk(growthBytes int64, t Thresholds) (riskLevel string, reasons []string) {
	switch {
	case growthBytes <= 0:
		return RiskLow, nil
	case growthBytes >= t.FailureBytes:
		return RiskHigh, []string{
			fmt.Sprintf("Total growth of %s reaches the failure threshold of %s.",
				HumanReadableSize(growthBytes), HumanReadableSize(t.FailureBytes)),
		}
	case growthBytes >= t.WarningBytes:
		return RiskMedium, []string{
			fmt.Sprintf("Total growth of %s reaches the warning threshold of %s.",
				HumanReadableSize(growthBytes), HumanReadableSize(t.WarningBytes)),
		}
	default:
		return RiskLow, nil
	}
}

// RecommendAction returns a human-readable recommendation for a medium or
// high risk change, based on whether one large file dominates the growth.
func RecommendAction(riskLevel string, largest *FileImpact, totalGrowth int64) string {
	if riskLevel == RiskLow || largest == nil {
		return ""
	}
	if largest.GrowthBytes*2 < totalGrowth {
		return "Many files add up to this growth. Check for build output, vendored dependencies, or data files that belong in .gitignore or Git LFS."
	}
	name := largest.FilePath
	if riskLevel == RiskMedium {
		return fmt.Sprintf("Consider Git LFS for %s if it will change often: git stores every version in full.", name)
	}
	if largest.IsBinary {
		track := name
		if ext := path.Ext(name); ext != "" {
			track = "*" + ext
		}
		return fmt.Sprintf("Track %s with Git LFS (git lfs track %q), or keep it out of git (.gitignore) and store it in external artifact storage (S3, GCS, etc.).", name, track)
	}
	return fmt.Sprintf("%s is an unusually large text file. Consider generating it at build time, compressing it, or keeping it out of git.", name)
}
