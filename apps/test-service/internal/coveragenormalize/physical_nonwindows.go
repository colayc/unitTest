//go:build !windows

package coveragenormalize

import (
	"fmt"
	"os"
	"reflect"
)

func physicalSourceIdentity(path string) (physicalSourceID, error) {
	file, err := os.Open(path)
	if err != nil {
		return physicalSourceID{}, fmt.Errorf("%w: open identity: %v", ErrSourceIdentity, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return physicalSourceID{}, ErrSourceIdentity
	}
	system := reflect.Indirect(reflect.ValueOf(info.Sys()))
	device, deviceOK := identityField(system, "Dev")
	inode, inodeOK := identityField(system, "Ino")
	if !deviceOK || !inodeOK {
		return physicalSourceID{}, ErrSourceIdentity
	}
	return physicalSourceID{device: device, file: inode}, nil
}

func identityField(value reflect.Value, name string) (uint64, bool) {
	if value.Kind() != reflect.Struct {
		return 0, false
	}
	field := value.FieldByName(name)
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		integer := field.Int()
		return uint64(integer), integer >= 0
	default:
		return 0, false
	}
}
