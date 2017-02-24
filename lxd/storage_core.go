package main

import (
	"fmt"
)

type storageCoreInfo interface {
	StorageCoreInit() (*storageCore, error)
	GetStorageType() storageType
	GetStorageTypeName() string
	GetStorageTypeVersion() string
}

type storageCore struct {
	sType        storageType
	sTypeName    string
	sTypeVersion string
}

func (sc *storageCore) GetStorageType() storageType {
	return sc.sType
}

func (sc *storageCore) GetStorageTypeName() string {
	return sc.sTypeName
}

func (sc *storageCore) GetStorageTypeVersion() string {
	return sc.sTypeVersion
}

func storagePoolCoreInit(poolDriver string) (*storageCore, error) {
	sType, err := storageStringToType(poolDriver)
	if err != nil {
		return nil, err
	}

	var s storage
	switch sType {
	case storageTypeBtrfs:
		s = &storageBtrfs{}
	case storageTypeZfs:
		s = &storageZfs{}
	case storageTypeLvm:
		s = &storageLvm{}
	case storageTypeDir:
		s = &storageDir{}
	case storageTypeMock:
		s = &storageMock{}
	default:
		return nil, fmt.Errorf("Unknown storage pool driver \"%s\".", poolDriver)
	}

	return s.StorageCoreInit()
}
