package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

func newLegacyEndpoint(lc fx.Lifecycle, shutdown fx.Shutdowner, cfg Config, logger *zap.Logger) *url.URL {
	target := &url.URL{Scheme: "http", Host: cfg.LegacyAddress}
	var process *exec.Cmd
	var stopped = make(chan struct{})
	var exited = make(chan struct{})
	var waitErr error

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			address, err := net.ResolveTCPAddr("tcp4", cfg.LegacyAddress)
			if err != nil {
				return err
			}
			listener, err := net.ListenTCP("tcp4", address)
			if err != nil {
				return err
			}
			defer listener.Close()
			socket, err := listener.File()
			if err != nil {
				return err
			}
			defer socket.Close()

			process = exec.Command(cfg.Python, "-m", "uvicorn", "legacy_app:app",
				"--fd", "3", "--no-proxy-headers", "--no-access-log", "--timeout-graceful-shutdown", "5")
			process.Dir = cfg.Workspace
			process.Env = append(os.Environ(), "EASEL_PORT="+target.Port())
			env := process.Env[:0]
			for _, entry := range process.Env {
				if !strings.HasPrefix(entry, "MANAGER_GATEWAY_TOKEN=") && !strings.HasPrefix(entry, "MANAGER_DATABASE_URL=") {
					env = append(env, entry)
				}
			}
			process.Env = env
			process.ExtraFiles = []*os.File{socket}
			process.Stdout, process.Stderr = os.Stdout, os.Stderr
			if err := process.Start(); err != nil {
				return err
			}
			go func() {
				waitErr = process.Wait()
				close(exited)
			}()
			// Release the manager's listener copies. The Python child owns the socket.
			_ = socket.Close()
			_ = listener.Close()
			if err := waitForEasel(ctx, target.String()+"/health", exited); err != nil {
				_ = process.Process.Kill()
				<-exited
				return err
			}
			go func() {
				<-exited
				select {
				case <-stopped:
					return
				default:
				}
				logger.Error("Easel compatibility process stopped", zap.Error(waitErr))
				_ = shutdown.Shutdown(fx.ExitCode(1))
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			close(stopped)
			if process == nil || process.Process == nil {
				return nil
			}
			_ = process.Process.Signal(os.Interrupt)
			select {
			case <-exited:
				return nil
			case <-ctx.Done():
				_ = process.Process.Kill()
				<-exited
				return ctx.Err()
			}
		},
	})
	return target
}

func waitForEasel(ctx context.Context, address string, exited <-chan struct{}) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		if response, err := client.Do(request); err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for Easel compatibility process: %w", ctx.Err())
		case <-exited:
			return errors.New("Easel compatibility process exited during startup")
		case <-ticker.C:
		}
	}
}
