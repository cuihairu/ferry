package proberole

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
)

func TestRoleExitsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, config.Config{}, log.New(io.Discard, "", 0))
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("role did not exit on cancel")
	}
}
