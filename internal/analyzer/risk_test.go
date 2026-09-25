package analyzer

import (
	"strings"
	"testing"
)

func TestClassifyRisk(t *testing.T) {
	th := Thresholds{WarningBytes: 50 * mib, FailureBytes: 100 * mib}

	tests := []struct {
		name       string
		growth     int64
		thresholds Thresholds
		wantRisk   string
		wantReason string
	}{
		{"zero growth", 0, th, RiskLow, ""},
		{"below warning", 10 * mib, th, RiskLow, ""},
		{"at warning", 50 * mib, th, RiskMedium, "warning threshold"},
		{"between", 75 * mib, th, RiskMedium, "warning threshold"},
		{"at failure", 100 * mib, th, RiskHigh, "failure threshold"},
		{"above failure", 500 * mib, th, RiskHigh, "failure threshold"},
		{"zero failure threshold still ignores zero growth", 0, Thresholds{}, RiskLow, ""},
		{"zero failure threshold blocks any growth", 1, Thresholds{}, RiskHigh, "failure threshold"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			risk, reasons := ClassifyRisk(tc.growth, tc.thresholds)
			if risk != tc.wantRisk {
				t.Errorf("risk = %q, want %q", risk, tc.wantRisk)
			}
			if tc.wantReason == "" {
				if len(reasons) != 0 {
					t.Errorf("expected no reasons, got %v", reasons)
				}
			} else if len(reasons) != 1 || !strings.Contains(reasons[0], tc.wantReason) {
				t.Errorf("reasons = %v, want one mentioning %q", reasons, tc.wantReason)
			}
		})
	}
}

func TestThresholdsFromMB(t *testing.T) {
	tests := []struct {
		warning, failure int
		wantErr          string
	}{
		{50, 100, ""},
		{0, 0, ""},
		{100, 100, ""},
		{-1, 100, "negative"},
		{50, -1, "negative"},
		{200, 100, "exceeds"},
		{50, MaxThresholdMB + 1, "at most"},
	}
	for _, tc := range tests {
		th, err := ThresholdsFromMB(tc.warning, tc.failure)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("ThresholdsFromMB(%d, %d): unexpected error %v", tc.warning, tc.failure, err)
			} else if th.WarningBytes != int64(tc.warning)*mib || th.FailureBytes != int64(tc.failure)*mib {
				t.Errorf("ThresholdsFromMB(%d, %d) = %+v", tc.warning, tc.failure, th)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("ThresholdsFromMB(%d, %d) error = %v, want %q", tc.warning, tc.failure, err, tc.wantErr)
		}
	}
}

func TestRecommendAction(t *testing.T) {
	bin := &FileImpact{FilePath: "models/weights.bin", GrowthBytes: 200 * mib, IsBinary: true}
	text := &FileImpact{FilePath: "data/dump.sql", GrowthBytes: 200 * mib}
	small := &FileImpact{FilePath: "a.js", GrowthBytes: 1 * mib}

	tests := []struct {
		name    string
		risk    string
		largest *FileImpact
		total   int64
		want    string
	}{
		{"low", RiskLow, bin, 200 * mib, ""},
		{"no files", RiskHigh, nil, 0, ""},
		{"high binary", RiskHigh, bin, 200 * mib, `git lfs track "*.bin"`},
		{"high text", RiskHigh, text, 200 * mib, "large text file"},
		{"medium", RiskMedium, bin, 200 * mib, "if it will change often"},
		{"many small files", RiskHigh, small, 200 * mib, "Many files"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RecommendAction(tc.risk, tc.largest, tc.total)
			if tc.want == "" && got != "" {
				t.Errorf("expected no recommendation, got %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("recommendation = %q, want it to contain %q", got, tc.want)
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
