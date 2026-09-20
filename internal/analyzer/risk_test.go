package analyzer

import "testing"

func TestClassifyRisk(t *testing.T) {
	th := DefaultThresholds()

	tests := []struct {
		name          string
		growthBytes   int64
		wantRisk      string
		wantReasons   bool // true if we expect non-empty reasons
	}{
		{
			name:        "zero bytes is low risk",
			growthBytes: 0,
			wantRisk:    RiskLow,
			wantReasons: false,
		},
		{
			name:        "1 MB is low risk",
			growthBytes: 1 * 1024 * 1024,
			wantRisk:    RiskLow,
			wantReasons: false,
		},
		{
			name:        "just below warning threshold is low risk",
			growthBytes: th.WarningBytes - 1,
			wantRisk:    RiskLow,
			wantReasons: false,
		},
		{
			name:        "exactly at warning threshold is medium risk",
			growthBytes: th.WarningBytes,
			wantRisk:    RiskMedium,
			wantReasons: true,
		},
		{
			name:        "between warning and failure is medium risk",
			growthBytes: 75 * 1024 * 1024,
			wantRisk:    RiskMedium,
			wantReasons: true,
		},
		{
			name:        "just below failure threshold is medium risk",
			growthBytes: th.FailureBytes - 1,
			wantRisk:    RiskMedium,
			wantReasons: true,
		},
		{
			name:        "exactly at failure threshold is high risk",
			growthBytes: th.FailureBytes,
			wantRisk:    RiskHigh,
			wantReasons: true,
		},
		{
			name:        "well above failure threshold is high risk",
			growthBytes: 500 * 1024 * 1024,
			wantRisk:    RiskHigh,
			wantReasons: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			risk, reasons := ClassifyRisk(tc.growthBytes, th)
			if risk != tc.wantRisk {
				t.Errorf("ClassifyRisk(%d) risk = %q, want %q", tc.growthBytes, risk, tc.wantRisk)
			}
			if tc.wantReasons && len(reasons) == 0 {
				t.Error("expected non-empty reasons, got none")
			}
			if !tc.wantReasons && len(reasons) != 0 {
				t.Errorf("expected no reasons, got %v", reasons)
			}
		})
	}
}

func TestRecommendAction(t *testing.T) {
	sampleFile := &FileImpact{
		FilePath:   "assets/big.bin",
		SizeBytes:  200 * 1024 * 1024,
		IsBinary:   true,
		ChangeType: "added",
	}

	tests := []struct {
		name      string
		riskLevel string
		isBinary  bool
		largest   *FileImpact
		wantEmpty bool
		wantSnip  string // substring expected in the recommendation
	}{
		{
			name:      "high risk binary recommends Git LFS",
			riskLevel: RiskHigh,
			isBinary:  true,
			largest:   sampleFile,
			wantSnip:  "Git LFS",
		},
		{
			name:      "high risk text recommends build-time generation",
			riskLevel: RiskHigh,
			isBinary:  false,
			largest:   nil,
			wantSnip:  "generated at build time",
		},
		{
			name:      "medium risk recommends monitoring",
			riskLevel: RiskMedium,
			isBinary:  false,
			largest:   nil,
			wantSnip:  "Monitor this",
		},
		{
			name:      "low risk returns empty string",
			riskLevel: RiskLow,
			isBinary:  false,
			largest:   nil,
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := RecommendAction(tc.riskLevel, tc.isBinary, tc.largest)
			if tc.wantEmpty {
				if rec != "" {
					t.Errorf("expected empty recommendation, got %q", rec)
				}
				return
			}
			if rec == "" {
				t.Fatal("expected non-empty recommendation, got empty")
			}
			if !contains(rec, tc.wantSnip) {
				t.Errorf("recommendation %q does not contain %q", rec, tc.wantSnip)
			}
		})
	}
}

func TestDefaultThresholds(t *testing.T) {
	th := DefaultThresholds()
	if th.WarningBytes != 50*1024*1024 {
		t.Errorf("WarningBytes = %d, want %d", th.WarningBytes, 50*1024*1024)
	}
	if th.FailureBytes != 100*1024*1024 {
		t.Errorf("FailureBytes = %d, want %d", th.FailureBytes, 100*1024*1024)
	}
}

// contains checks if s contains substr (simple helper to avoid importing strings in test).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
