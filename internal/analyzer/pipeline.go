package analyzer

import "fmt"

// AnalyzeFile is the main entry point that ties together size detection,
// binary detection, git object lookup, risk classification, and
// recommendation into a single Report for the given file.
func AnalyzeFile(repoPath string, filePath string, thresholds Thresholds) (*Report, error) {
	// Step 1: Get the file's on-disk size.
	sizeBytes, err := GetFileSize(filePath)
	if err != nil {
		return nil, fmt.Errorf("get file size: %w", err)
	}

	// Step 2: Determine if the file is binary.
	isBinary, err := IsLikelyBinary(filePath)
	if err != nil {
		return nil, fmt.Errorf("binary detection: %w", err)
	}

	// Step 3: Check if this exact content already exists in the object store.
	// If it does, the growth is effectively zero (deduplication).
	var growthBytes int64
	alreadyExists, err := ObjectAlreadyExists(repoPath, filePath)
	if err != nil {
		return nil, fmt.Errorf("object existence check: %w", err)
	}

	if alreadyExists {
		growthBytes = 0
	} else {
		growthBytes, err = EstimateCompressedSize(repoPath, filePath)
		if err != nil {
			return nil, fmt.Errorf("estimate compressed size: %w", err)
		}
	}

	// Step 4: Classify risk based on growth.
	riskLevel, reasons := ClassifyRisk(growthBytes, thresholds)

	// Step 5: Build a FileImpact for this file.
	// ChangeType is hardcoded to "added" for now; later prompts will make
	// this dynamic based on git diff status.
	impact := FileImpact{
		FilePath:   filePath,
		SizeBytes:  sizeBytes,
		IsBinary:   isBinary,
		ChangeType: "added",
	}

	// Step 6: Build the recommendation.
	recommendation := RecommendAction(riskLevel, isBinary, &impact)

	// Step 7: Assemble and return the report.
	report := &Report{
		RiskLevel:      riskLevel,
		GrowthBytes:    growthBytes,
		Files:          []FileImpact{impact},
		LargestFile:    &impact,
		Recommendation: recommendation,
		Reasons:        reasons,
	}

	return report, nil
}
