package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type JsonSerial struct{}

func (s JsonSerial) Load(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(file)
	var m map[string]interface{}
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}

	dict := make(map[string]string)
	for k, v := range m {
		switch v.(type) {
		case string:
			dict[k] = v.(string)
		case []interface{}, map[string]interface{}:
			return nil, errors.New("nested arrays and objects are not supported. json key " + k)
		default:
			dict[k] = fmt.Sprintf("%v", v)
		}
	}

	return dict, nil
}

func (s JsonSerial) Save(path string, dict *map[string]string) error {
	file, err := os.Create(path)

	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	return enc.Encode(*dict)
}

func init() {
	RegisterSerial(JsonSerial{}, ".json")
}
