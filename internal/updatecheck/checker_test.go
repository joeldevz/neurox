package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	fixedTime := time.Date(2026, time.March, 29, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(t *testing.T, configDir string)
		want    CachedResult
		wantErr string
	}{
		{
			name:  "missing file returns zero value",
			setup: func(t *testing.T, configDir string) {},
			want:  CachedResult{},
		},
		{
			name: "valid load",
			setup: func(t *testing.T, configDir string) {
				if err := os.MkdirAll(configDir, 0o755); err != nil {
					t.Fatalf("MkdirAll returned error: %v", err)
				}

				payload := CachedResult{
					LatestVersion:  "1.4.0",
					CheckedAt:      fixedTime,
					CurrentVersion: "1.3.0",
				}

				data, err := json.Marshal(payload)
				if err != nil {
					t.Fatalf("Marshal returned error: %v", err)
				}

				if err := os.WriteFile(cachePath(configDir), data, 0o644); err != nil {
					t.Fatalf("WriteFile returned error: %v", err)
				}
			},
			want: CachedResult{
				LatestVersion:  "1.4.0",
				CheckedAt:      fixedTime,
				CurrentVersion: "1.3.0",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configDir := filepath.Join(t.TempDir(), "config")
			tc.setup(t, configDir)

			got, err := Load(configDir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load returned error: %v", err)
			}

			if got != tc.want {
				t.Fatalf("Load = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	want := CachedResult{
		LatestVersion:  "2.0.0",
		CheckedAt:      time.Date(2026, time.March, 29, 12, 0, 0, 0, time.UTC),
		CurrentVersion: "1.9.0",
	}

	if err := Save(configDir, want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := Load(configDir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got != want {
		t.Fatalf("roundtrip result = %+v, want %+v", got, want)
	}
}

func TestCheck(t *testing.T) {
	fixedNow := time.Date(2026, time.March, 29, 13, 0, 0, 0, time.UTC)
	originalNow := now
	originalResolveLatestVersion := resolveLatestVersion
	t.Cleanup(func() {
		now = originalNow
		resolveLatestVersion = originalResolveLatestVersion
	})

	tests := []struct {
		name              string
		currentVersion    string
		seedCache         *CachedResult
		resolverLatest    string
		resolverErr       error
		wantLatest        string
		wantNewer         bool
		wantErr           string
		wantResolverCalls int
		wantCache         *CachedResult
	}{
		{
			name:           "cache hit",
			currentVersion: "1.0.0",
			seedCache: &CachedResult{
				LatestVersion:  "1.2.0",
				CheckedAt:      fixedNow.Add(-2 * time.Hour),
				CurrentVersion: "1.0.0",
			},
			wantLatest:        "1.2.0",
			wantNewer:         true,
			wantResolverCalls: 0,
			wantCache: &CachedResult{
				LatestVersion:  "1.2.0",
				CheckedAt:      fixedNow.Add(-2 * time.Hour),
				CurrentVersion: "1.0.0",
			},
		},
		{
			name:           "stale cache refresh",
			currentVersion: "1.0.0",
			seedCache: &CachedResult{
				LatestVersion:  "1.1.0",
				CheckedAt:      fixedNow.Add(-25 * time.Hour),
				CurrentVersion: "1.0.0",
			},
			resolverLatest:    "1.3.0",
			wantLatest:        "1.3.0",
			wantNewer:         true,
			wantResolverCalls: 1,
			wantCache: &CachedResult{
				LatestVersion:  "1.3.0",
				CheckedAt:      fixedNow,
				CurrentVersion: "1.0.0",
			},
		},
		{
			name:              "same version false",
			currentVersion:    "1.3.0",
			resolverLatest:    "1.3.0",
			wantLatest:        "1.3.0",
			wantNewer:         false,
			wantResolverCalls: 1,
			wantCache: &CachedResult{
				LatestVersion:  "1.3.0",
				CheckedAt:      fixedNow,
				CurrentVersion: "1.3.0",
			},
		},
		{
			name:              "newer version true",
			currentVersion:    "1.3.0",
			resolverLatest:    "1.4.0",
			wantLatest:        "1.4.0",
			wantNewer:         true,
			wantResolverCalls: 1,
			wantCache: &CachedResult{
				LatestVersion:  "1.4.0",
				CheckedAt:      fixedNow,
				CurrentVersion: "1.3.0",
			},
		},
		{
			name:              "network error",
			currentVersion:    "1.3.0",
			resolverErr:       errors.New("boom"),
			wantErr:           "resolve latest version: boom",
			wantResolverCalls: 1,
			wantCache:         &CachedResult{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now = func() time.Time { return fixedNow }

			resolverCalls := 0
			resolveLatestVersion = func(ctx context.Context, currentVersion string) (string, error) {
				resolverCalls++
				if tc.resolverErr != nil {
					return "", tc.resolverErr
				}
				return tc.resolverLatest, nil
			}

			configDir := filepath.Join(t.TempDir(), "config")
			if tc.seedCache != nil {
				if err := Save(configDir, *tc.seedCache); err != nil {
					t.Fatalf("seed Save returned error: %v", err)
				}
			}

			gotLatest, gotNewer, err := Check(context.Background(), tc.currentVersion, configDir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Check error = %v, want substring %q", err, tc.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("Check returned error: %v", err)
				}
				if gotLatest != tc.wantLatest {
					t.Fatalf("latestVersion = %q, want %q", gotLatest, tc.wantLatest)
				}
				if gotNewer != tc.wantNewer {
					t.Fatalf("isNewer = %t, want %t", gotNewer, tc.wantNewer)
				}
			}

			if resolverCalls != tc.wantResolverCalls {
				t.Fatalf("resolver calls = %d, want %d", resolverCalls, tc.wantResolverCalls)
			}

			cached, loadErr := Load(configDir)
			if loadErr != nil {
				t.Fatalf("Load returned error: %v", loadErr)
			}

			if tc.wantCache != nil && cached != *tc.wantCache {
				t.Fatalf("cached result = %+v, want %+v", cached, *tc.wantCache)
			}
		})
	}
}
