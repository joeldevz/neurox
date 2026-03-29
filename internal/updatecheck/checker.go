package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/installer"
)

const (
	cacheFileName = "update-check.json"
	cacheTTL      = 24 * time.Hour
)

type CachedResult struct {
	LatestVersion  string    `json:"latest_version"`
	CheckedAt      time.Time `json:"checked_at"`
	CurrentVersion string    `json:"current_version"`
}

var now = time.Now

var resolveLatestVersion = func(ctx context.Context, currentVersion string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	latestVersion, _, err := installer.CheckLatest(currentVersion)
	if err != nil {
		return "", fmt.Errorf("check latest release: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	return latestVersion, nil
}

func Load(configDir string) (CachedResult, error) {
	path := cachePath(configDir)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CachedResult{}, nil
		}
		return CachedResult{}, fmt.Errorf("read update cache %s: %w", path, err)
	}

	var result CachedResult
	if err := json.Unmarshal(data, &result); err != nil {
		return CachedResult{}, fmt.Errorf("parse update cache %s: %w", path, err)
	}

	return result, nil
}

func Save(configDir string, r CachedResult) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", configDir, err)
	}

	path := cachePath(configDir)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal update cache: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}

	tmpPath := tmpFile.Name()
	success := false
	defer func() {
		_ = tmpFile.Close()
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write temp cache file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp cache file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp cache file: %w", err)
	}

	success = true
	return nil
}

func Check(ctx context.Context, currentVersion, configDir string) (latestVersion string, isNewer bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}

	cached, err := Load(configDir)
	if err != nil {
		return "", false, fmt.Errorf("load update cache: %w", err)
	}

	currentTime := now().UTC()
	if shouldUseCache(cached, currentVersion, currentTime) {
		return cached.LatestVersion, isVersionNewer(cached.LatestVersion, currentVersion), nil
	}

	latestVersion, err = resolveLatestVersion(ctx, currentVersion)
	if err != nil {
		return "", false, fmt.Errorf("resolve latest version: %w", err)
	}

	result := CachedResult{
		LatestVersion:  latestVersion,
		CheckedAt:      currentTime,
		CurrentVersion: currentVersion,
	}
	if err := Save(configDir, result); err != nil {
		return "", false, fmt.Errorf("save update cache: %w", err)
	}

	return latestVersion, isVersionNewer(latestVersion, currentVersion), nil
}

func cachePath(configDir string) string {
	return filepath.Join(configDir, cacheFileName)
}

func shouldUseCache(cached CachedResult, currentVersion string, currentTime time.Time) bool {
	if cached.CheckedAt.IsZero() {
		return false
	}
	if cached.CurrentVersion != currentVersion {
		return false
	}

	return !currentTime.After(cached.CheckedAt.Add(cacheTTL))
}

func isVersionNewer(latestVersion, currentVersion string) bool {
	comparison := compareVersions(latestVersion, currentVersion)
	return comparison > 0
}

func compareVersions(left, right string) int {
	leftParts := parseVersion(left)
	rightParts := parseVersion(right)
	maxLen := len(leftParts)
	if len(rightParts) > maxLen {
		maxLen = len(rightParts)
	}

	for i := range maxLen {
		leftValue := 0
		if i < len(leftParts) {
			leftValue = leftParts[i]
		}

		rightValue := 0
		if i < len(rightParts) {
			rightValue = rightParts[i]
		}

		switch {
		case leftValue > rightValue:
			return 1
		case leftValue < rightValue:
			return -1
		}
	}

	return 0
}

func parseVersion(version string) []int {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, ".")
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		parsed = append(parsed, parseVersionPart(part))
	}

	return parsed
}

func parseVersionPart(part string) int {
	end := 0
	for end < len(part) && part[end] >= '0' && part[end] <= '9' {
		end++
	}

	if end == 0 {
		return 0
	}

	value, err := strconv.Atoi(part[:end])
	if err != nil {
		return 0
	}

	return value
}
