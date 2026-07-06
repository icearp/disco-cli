// Command disco is a cloud-resource discovery CLI — see CLAUDE.md and
// the package docs under cmd/ for usage. main only dispatches to cmd.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"codeberg.org/icearp/disco/cmd"
)

func main() {
	// Cancel the command's context on first SIGINT/SIGTERM so a running scan
	// unwinds gracefully and its deferred store.Close() runs the WAL
	// checkpoint+cleanup (no orphaned -wal/-shm sidecars); a second signal
	// restores the default handler and force-kills if shutdown hangs.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	cmd.Execute(ctx)
}
