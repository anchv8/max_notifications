//go:build !windows

package main

import "context"

func checkIsService() bool { return false }

func runAsService(_ func(ctx context.Context)) bool { return false }
