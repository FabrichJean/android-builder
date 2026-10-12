//go:build windows

package server

import "errors"

func freeDiskBytes(string) (uint64, error) { return 0, errors.New("non supporté sur Windows") }
