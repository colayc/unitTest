package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const maxTokenFileBytes = 4096

func consumeTokenFile(path string) (token string, err error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		removeErr := os.Remove(path)
		return "", errors.Join(errors.New("authentication token file must not be a symlink"), removeErr)
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("authentication token file must be a regular non-symlink file")
	}

	file, err := openTokenFile(path)
	if err != nil {
		return "", errors.Join(err, removeSameTokenFile(path, before))
	}

	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", errors.Join(err, removeSameTokenFile(path, before))
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return "", errors.Join(errors.New("authentication token file changed while opening"), removeSameTokenFile(path, before))
	}
	defer func() {
		cleanupErr := removeSameTokenFile(path, after)
		closeErr := file.Close()
		if cleanupErr != nil || closeErr != nil {
			token = ""
			err = errors.Join(err, cleanupErr, closeErr)
		}
	}()
	if err := validateTokenFile(file, after); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxTokenFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxTokenFileBytes {
		return "", fmt.Errorf("authentication token file exceeds %d bytes", maxTokenFileBytes)
	}
	token = strings.TrimSpace(string(raw))
	if len(token) < 16 {
		return "", errors.New("authentication token must contain at least 16 characters")
	}
	return token, nil
}

func removeSameTokenFile(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect authentication token file before deletion: %w", err)
	}
	if !os.SameFile(expected, current) {
		return errors.New("authentication token file changed before deletion")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete authentication token file: %w", err)
	}
	return nil
}
