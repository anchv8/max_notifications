//go:build !windows

package main

import "context"

func runAsService(_ func(ctx context.Context)) bool {
	return false
}
