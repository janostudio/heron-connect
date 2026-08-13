//go:build windows

package main

import (
	"context"

	"github.com/janostudio/heron-connect/config"
)

func runRunAsUserStartupChecks(_ context.Context, _ *config.Config) error {
	return nil
}
