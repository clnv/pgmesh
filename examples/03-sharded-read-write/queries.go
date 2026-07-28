package main

import (
	"context"
	"fmt"

	"github.com/clnv/pgmesh/examples/internal/one"
	"github.com/clnv/pgmesh/examples/internal/sharded"
)

func exerciseStores(
	ctx context.Context,
	accounts sharded.Accounts,
	reports sharded.ReportsReader,
	settings one.Settings,
) error {
	account, err := accounts.UpsertAccount(ctx, &sharded.UpsertAccountParams{
		ID:          3001,
		TenantID:    100,
		DisplayName: "shard one",
	})
	if err != nil {
		return fmt.Errorf("routed write: %w", err)
	}
	account, err = accounts.GetAccount(
		ctx,
		&sharded.GetAccountParams{TenantID: account.TenantID, ID: account.ID},
		sharded.ReadFromPrimary(),
	)
	if err != nil {
		return fmt.Errorf("routed primary read: %w", err)
	}
	fmt.Printf("tenant %d account: %s\n", account.TenantID, account.DisplayName)

	count, err := reports.CountAccounts(
		ctx,
		account.TenantID,
		sharded.ReadFromPrimary(),
	)
	if err != nil {
		return fmt.Errorf("count tenant accounts: %w", err)
	}
	fmt.Printf("tenant %d account count: %d\n", account.TenantID, count)

	_, err = settings.UpsertSetting(ctx, &one.UpsertSettingParams{
		Key:   "deployment_name",
		Value: "pgmesh example",
	})
	if err != nil {
		return fmt.Errorf("unsharded setting write: %w", err)
	}
	setting, err := settings.GetSetting(ctx, "deployment_name")
	if err != nil {
		return fmt.Errorf("unsharded setting read: %w", err)
	}
	fmt.Printf("deployment: %s\n", setting.Value)
	return nil
}
