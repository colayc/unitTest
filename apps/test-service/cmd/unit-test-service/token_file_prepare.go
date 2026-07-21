package main

import (
	"errors"
)

func prepareTokenFile(path string) error {
	file, err := createTokenFile(path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		return errors.Join(statErr, file.Close())
	}
	validationErr := validateTokenFile(file, info)
	closeErr := file.Close()
	if validationErr == nil && closeErr == nil {
		return nil
	}
	cleanupErr := removeSameTokenFile(path, info)
	return errors.Join(validationErr, closeErr, cleanupErr)
}
