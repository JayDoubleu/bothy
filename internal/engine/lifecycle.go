package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// NewStamp returns a fresh readiness-stamp nonce (see LabelStamp).
func NewStamp() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("cannot generate readiness stamp: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ReadyStampPath returns the host-side path of the readiness stamp that
// the init process writes once first-boot setup is complete. The stamp
// lives in the bothy home because that is a bind mount visible on both
// sides; container-only paths like /run are not. The nonce suffix prevents
// a stale stamp (from a previous container recreated over the same home
// with --keep-home) from passing the readiness check.
func ReadyStampPath(homeDir, stamp string) string {
	return filepath.Join(homeDir, ".cache", "bothy", "ready-"+stamp)
}

// WaitReady polls for the readiness stamp until it appears or the context
// expires.
func WaitReady(ctx context.Context, path string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for container initialization (readiness stamp %s never appeared)", path)
		case <-ticker.C:
		}
	}
}
