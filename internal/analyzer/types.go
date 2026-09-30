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

// ChangeType values reported in FileImpact.
const (
	ChangeAdded    = "added"    // untracked file that would be added
	ChangeModified = "modified" // tracked file with unstaged changes
	ChangeStaged   = "staged"   // content already in the index
	ChangeFile     = "file"     // file named explicitly with --file
)

// FileImpact represents the size impact of a single file.
type FileImpact struct {
	FilePath    string `json:"file_path"`    // relative to the repository root
	SizeBytes   int64  `json:"size_bytes"`   // size in the working tree (or of the staged blob)
	GrowthBytes int64  `json:"growth_bytes"` // bytes git would newly store; 0 if the content is already in HEAD
	IsBinary    bool   `json:"is_binary"`
	ChangeType  string `json:"change_type"`
}

// Report is the top-level JSON report produced by gitsize-guard.
type Report struct {
	RiskLevel      string       `json:"risk_level"` // "low", "medium", or "high"
	GrowthBytes    int64        `json:"growth_bytes"`
	FilesAnalyzed  int          `json:"files_analyzed"`
	Files          []FileImpact `json:"files"`        // largest growth first, at most maxReportedFiles
	LargestFile    *FileImpact  `json:"largest_file"` // nullable
	Recommendation string       `json:"recommendation"`
	Reasons        []string     `json:"reasons"`
}

// ToJSON marshals the report to indented JSON.
func (r *Report) ToJSON() (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
