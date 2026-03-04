//go:build windows

package main

// This file provides the shutdown context for Windows.
//
// WHY a platform-specific file instead of runtime.GOOS check in main.go:
//   golang.org/x/sys/windows/svc is a Windows-only package that does not compile
//   on Linux/macOS. A build-tagged file is the idiomatic Go way to isolate
//   platform-specific imports without conditional compilation inside main().
//
// Behaviour:
//   - Running as Windows service (SCM launched): makeShutdownCtx() returns a
//     context that is cancelled when the SCM sends Stop/Shutdown. The winSvcHandler
//     reports StartPending → Running to SCM on startup and StopPending on shutdown,
//     so the SCM never reports "service did not respond in a timely fashion".
//   - Running interactively (console / dev): falls back to signal.NotifyContext
//     so Ctrl+C / SIGTERM still work as expected.
//
// PREVIOUSLY TRIED: using signal.NotifyContext alone on Windows.
//   When running as a Windows service, the SCM sends a service control code (not a
//   signal). signal.NotifyContext never fires → the service hangs on sc stop / system
//   shutdown, and the SCM forcibly kills the process after its timeout, bypassing the
//   30-second graceful drain in main().

import (
	"context"
	"log/slog"
	"os/signal"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows/svc"
)

const windowsServiceName = "dnshenet-server"

// makeShutdownCtx returns a context that is cancelled on service stop (when running
// under the Windows SCM) or on SIGTERM/SIGINT (when running interactively).
func makeShutdownCtx() (context.Context, context.CancelFunc) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		slog.Warn("cannot detect Windows service mode, using signal-based shutdown", "error", err)
		isService = false
	}

	if isService {
		ctx, cancel := context.WithCancel(context.Background())
		// registered is closed by winSvcHandler.Execute() after SERVICE_RUNNING is sent,
		// so the caller can block until the SCM has acknowledged the service is up.
		registered := make(chan struct{})
		go func() {
			// WHY LockOSThread:
			//   RegisterServiceCtrlHandlerExW (called inside svc.Run) registers a
			//   per-thread callback with the Windows SCM. Locking the goroutine to
			//   an OS thread ensures the callback dispatch stays on the same thread
			//   for the lifetime of the service, matching Windows' expectations.
			runtime.LockOSThread()
			writeEarlyDebug("svc.Run goroutine: calling svc.Run")
			if err := svc.Run(windowsServiceName, &winSvcHandler{cancel: cancel, registered: registered}); err != nil {
				writeEarlyDebug("svc.Run goroutine: ERROR " + err.Error())
				slog.Error("Windows service run error", "error", err)
			}
			writeEarlyDebug("svc.Run goroutine: svc.Run returned")
		}()
		// WHY block until registered:
		//   Without this, the main goroutine races ahead to browser.NewLauncher().
		//   If browser init is slow (Chromium GPU/sandbox init under LocalSystem),
		//   the SCM goroutine may not have sent SERVICE_RUNNING yet when the SCM
		//   timeout fires → Error 1053. Blocking here guarantees SERVICE_RUNNING
		//   reaches the SCM before any heavy initialisation starts.
		<-registered
		writeEarlyDebug("makeShutdownCtx: unblocked (SERVICE_RUNNING sent to SCM)")
		return ctx, cancel
	}

	// Interactive (console) mode — fall back to OS signals.
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

// winSvcHandler implements golang.org/x/sys/windows/svc.Handler.
// It bridges Windows SCM lifecycle events to a context.CancelFunc.
type winSvcHandler struct {
	cancel     context.CancelFunc
	registered chan struct{} // closed after SERVICE_RUNNING is sent; unblocks makeShutdownCtx caller
}

func (h *winSvcHandler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	// WHY StartPending before Running:
	//   The SCM requires at least one StartPending heartbeat while the service is
	//   initialising (browser launch + DB open can take 1-2 s). Without it, the SCM
	//   may report "service did not respond" in the Windows Event Log.
	status <- svc.Status{State: svc.StartPending}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	// Signal makeShutdownCtx that SERVICE_RUNNING has been sent. The main goroutine
	// is blocked on <-registered, so it cannot start browser init before this point.
	close(h.registered)

	for c := range req {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			// WHY cancel() before StopPending:
			//   cancel() triggers main()'s graceful drain (30 s window). We immediately
			//   report StopPending so the SCM knows we are shutting down and does not
			//   consider us frozen while we wait for in-flight browser operations.
			h.cancel()
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		case svc.Interrogate:
			status <- c.CurrentStatus
		}
	}
	return false, 0
}
