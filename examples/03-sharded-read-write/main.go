package main

import (
	"context"
	"log"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	pools := newPoolRegistry()
	defer pools.close()

	accounts, err := newAccountQueries(ctx, cfg, pools)
	if err != nil {
		return err
	}
	settings, err := newSettingsStore(ctx, cfg.settingsDSN, pools)
	if err != nil {
		return err
	}
	return exerciseStores(
		ctx,
		accounts.Accounts(),
		accounts.Reports(),
		settings.Settings(),
	)
}
