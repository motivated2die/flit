package main

import (
	"encoding/json"
	"os"

	"github.com/motivated2die/flit/internal/engine/native"
)

func readJSONContract(path string) (native.Contract, error) {
	var c native.Contract
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}
