package main

import (
	"errors"
	"os"
)

var statPreparedTokenFile = func(file *os.File) (os.FileInfo, error) {
	return file.Stat()
}

func prepareTokenFile(path string) error {
	file, createdInfo, err := createTokenFile(path)
	if err != nil {
		return err
	}
	info, statErr := statPreparedTokenFile(file)
	if statErr != nil {
		closeErr := file.Close()
		cleanupErr := removeSameTokenFile(path, createdInfo)
		return errors.Join(statErr, closeErr, cleanupErr)
	}
	if !os.SameFile(createdInfo, info) {
		closeErr := file.Close()
		cleanupErr := removeSameTokenFile(path, createdInfo)
		return errors.Join(errors.New("prepared authentication token file changed after creation"), closeErr, cleanupErr)
	}
	validationErr := validateTokenFile(file, info)
	closeErr := file.Close()
	if validationErr == nil && closeErr == nil {
		return nil
	}
	cleanupErr := removeSameTokenFile(path, info)
	return errors.Join(validationErr, closeErr, cleanupErr)
}
