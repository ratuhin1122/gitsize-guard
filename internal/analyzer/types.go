package analyzer

import (
	"encoding/json"
)

// RiskLevel constants define the possible risk levels for a report.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// FileImpact represents the size impact of a single file in a commit.
type FileImpact struct {
	FilePath   string `json:"file_path"`
	SizeBytes  int64  `json:"size_bytes"`
	IsBinary   bool   `json:"is_binary"`
	ChangeType string `json:"change_type"` // "added", "modified", or "deleted"
}

// Report is the top-level JSON report produced by gitsize-guard.
type Report struct {
	RiskLevel      string      `json:"risk_level"`      // "low", "medium", or "high"
	GrowthBytes    int64       `json:"growth_bytes"`
	Files          []FileImpact `json:"files"`
	LargestFile    *FileImpact `json:"largest_file"`    // nullable
	Recommendation string      `json:"recommendation"`
	Reasons        []string    `json:"reasons"`
}

// ToJSON marshals the report to indented JSON.
func (r *Report) ToJSON() (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
