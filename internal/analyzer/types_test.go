package analyzer

import (
	"encoding/json"
	"testing"
)

func TestReportToJSON(t *testing.T) {
	largest := FileImpact{
		FilePath:   "assets/logo.png",
		SizeBytes:  2_500_000,
		IsBinary:   true,
		ChangeType: "added",
	}

	tests := []struct {
		name   string
		report Report
		checks func(t *testing.T, parsed map[string]interface{})
	}{
		{
			name: "high risk report with largest file",
			report: Report{
				RiskLevel:   RiskHigh,
				GrowthBytes: 3_000_000,
				Files: []FileImpact{
					{FilePath: "main.go", SizeBytes: 500_000, IsBinary: false, ChangeType: "modified"},
					largest,
				},
				LargestFile:    &largest,
				Recommendation: "Consider using Git LFS for large binary files.",
				Reasons:        []string{"binary file over 1 MB", "total growth exceeds 2 MB"},
			},
			checks: func(t *testing.T, parsed map[string]interface{}) {
				if parsed["risk_level"] != "high" {
					t.Errorf("expected risk_level=high, got %v", parsed["risk_level"])
				}
				if parsed["growth_bytes"].(float64) != 3_000_000 {
					t.Errorf("expected growth_bytes=3000000, got %v", parsed["growth_bytes"])
				}
				files, ok := parsed["files"].([]interface{})
				if !ok || len(files) != 2 {
					t.Fatalf("expected 2 files, got %v", parsed["files"])
				}
				if parsed["largest_file"] == nil {
					t.Error("expected largest_file to be non-nil")
				}
				reasons, ok := parsed["reasons"].([]interface{})
				if !ok || len(reasons) != 2 {
					t.Fatalf("expected 2 reasons, got %v", parsed["reasons"])
				}
			},
		},
		{
			name: "low risk report with nil largest file",
			report: Report{
				RiskLevel:      RiskLow,
				GrowthBytes:    1024,
				Files:          []FileImpact{{FilePath: "readme.md", SizeBytes: 1024, IsBinary: false, ChangeType: "added"}},
				LargestFile:    nil,
				Recommendation: "",
				Reasons:        nil,
			},
			checks: func(t *testing.T, parsed map[string]interface{}) {
				if parsed["risk_level"] != "low" {
					t.Errorf("expected risk_level=low, got %v", parsed["risk_level"])
				}
				if parsed["largest_file"] != nil {
					t.Error("expected largest_file to be null")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.report.ToJSON()
			if err != nil {
				t.Fatalf("ToJSON() returned error: %v", err)
			}

			// Verify the output is valid JSON
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("ToJSON() produced invalid JSON: %v\noutput: %s", err, out)
			}

			tc.checks(t, parsed)
		})
	}
}
