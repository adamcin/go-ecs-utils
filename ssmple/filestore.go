package main

import (
	"os"
	"path/filepath"
)

type FileStore struct {
	Path string
	Dict map[string]string
}

func (fs *FileStore) Load() error {
	serial := GetSerialFor(fs.Path)
	dict, err := serial.Load(fs.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	} else {
		fs.Dict = dict
		return nil
	}
}

func (fs *FileStore) Save() error {
	serial := GetSerialFor(fs.Path)
	return serial.Save(fs.Path, &fs.Dict)
}

func NewFileStore(confDir string, filename string) FileStore {
	path := filepath.Join(confDir, filename)
	dict := make(map[string]string, 0)

	store := FileStore{
		Path: path,
		Dict: dict}

	return store
}
