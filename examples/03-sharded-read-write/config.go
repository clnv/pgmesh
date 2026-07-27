package main

import (
	"errors"
	"os"
)

type config struct {
	shard0Primary string
	shard0Replica string
	shard1Primary string
	shard1Replica string
	settingsDSN   string
}

func loadConfig() (config, error) {
	names := []string{
		"SHARD0_PRIMARY_DSN",
		"SHARD0_REPLICA_DSN",
		"SHARD1_PRIMARY_DSN",
		"SHARD1_REPLICA_DSN",
		"SETTINGS_DSN",
	}
	values := make([]string, len(names))
	for index, name := range names {
		values[index] = os.Getenv(name)
		if values[index] == "" {
			return config{}, errors.New(name + " is required")
		}
	}
	return config{
		shard0Primary: values[0],
		shard0Replica: values[1],
		shard1Primary: values[2],
		shard1Replica: values[3],
		settingsDSN:   values[4],
	}, nil
}
