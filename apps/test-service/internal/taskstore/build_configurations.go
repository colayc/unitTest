package taskstore

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

type BuildConfiguration struct {
	WorkspaceID     string
	ProjectID       string
	ProfileID       string
	Fingerprint     string
	BuildDirectory  string
	CMakeIdentity   string
	FileAPIIdentity string
	ConfiguredAt    time.Time
}

func (s *Store) PutBuildConfiguration(ctx context.Context, value BuildConfiguration) error {
	if s == nil || ctx == nil || !validBuildConfiguration(value) {
		return task.ErrInvalidArgument
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO build_configurations(
		  workspace_id, project_id, profile_id, configure_fingerprint,
		  build_directory, cmake_identity, file_api_identity, configured_at
		) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(workspace_id, project_id, profile_id) DO UPDATE SET
		  configure_fingerprint=excluded.configure_fingerprint,
		  build_directory=excluded.build_directory,
		  cmake_identity=excluded.cmake_identity,
		  file_api_identity=excluded.file_api_identity,
		  configured_at=excluded.configured_at`,
		value.WorkspaceID, value.ProjectID, value.ProfileID, value.Fingerprint,
		value.BuildDirectory, value.CMakeIdentity, value.FileAPIIdentity,
		formatTime(value.ConfiguredAt),
	)
	if err != nil {
		return storageError("put build configuration", err)
	}
	return nil
}

func (s *Store) GetBuildConfiguration(
	ctx context.Context,
	workspaceID string,
	projectID string,
	profileID string,
) (BuildConfiguration, error) {
	if s == nil || ctx == nil || !lowerHex(workspaceID, 64) || !validProjectID(projectID) ||
		!lowerHex(profileID, 64) {
		return BuildConfiguration{}, task.ErrInvalidArgument
	}
	var result BuildConfiguration
	var configuredAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT workspace_id, project_id, profile_id, configure_fingerprint,
		       build_directory, cmake_identity, file_api_identity, configured_at
		FROM build_configurations
		WHERE workspace_id=? AND project_id=? AND profile_id=?`,
		workspaceID, projectID, profileID,
	).Scan(
		&result.WorkspaceID, &result.ProjectID, &result.ProfileID,
		&result.Fingerprint, &result.BuildDirectory, &result.CMakeIdentity,
		&result.FileAPIIdentity, &configuredAt,
	)
	if isNoRows(err) {
		return BuildConfiguration{}, task.ErrNotFound
	}
	if err != nil {
		return BuildConfiguration{}, storageError("get build configuration", err)
	}
	result.ConfiguredAt, err = time.Parse(time.RFC3339Nano, configuredAt)
	if err != nil || !validBuildConfiguration(result) {
		return BuildConfiguration{}, storageError("decode build configuration", err)
	}
	return result, nil
}

func validBuildConfiguration(value BuildConfiguration) bool {
	return lowerHex(value.WorkspaceID, 64) && validProjectID(value.ProjectID) &&
		lowerHex(value.ProfileID, 64) && lowerHex(value.Fingerprint, 64) &&
		validBuildDirectoryIdentity(value.BuildDirectory) &&
		lowerHex(value.CMakeIdentity, 64) &&
		lowerHex(value.FileAPIIdentity, 64) && !value.ConfiguredAt.IsZero()
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return !strings.ContainsRune(value, 0)
}

func validProjectID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func validBuildDirectoryIdentity(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) ||
		filepath.IsAbs(filepath.FromSlash(value)) ||
		filepath.Clean(filepath.FromSlash(value)) != filepath.FromSlash(value) {
		return false
	}
	return value != ".." && !strings.HasPrefix(value, "../")
}
