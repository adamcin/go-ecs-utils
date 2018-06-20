package main

import (
	"github.com/rickar/props"
	"os"
	"path/filepath"
)

type Serial interface {
	Load(path string) (map[string]string, error)
	Save(path string, dict *map[string]string) error
}

type PropsSerial struct{}

func (s PropsSerial) Load(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	p := props.NewProperties()
	p.Load(file)

	dict := make(map[string]string, len(p.Names()))
	names := p.Names()
	for i := range names {
		name := names[i]
		dict[name] = p.Get(name)
	}

	return dict, nil
}

func (s PropsSerial) Save(path string, dict *map[string]string) error {
	p := props.NewProperties()
	for key, value := range *dict {
		p.Set(key, value)
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}

	return p.Write(file)
}

var serials = make(map[string]Serial, 0)

func GetSerialFor(path string) Serial {
	ext := filepath.Ext(path)
	serial := serials[""]
	if extSerial, ok := serials[ext]; ok {
		serial = extSerial
	}
	return serial
}

func init() {
	serials[""] = PropsSerial{}
}
