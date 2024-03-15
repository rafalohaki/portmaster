package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tevino/abool"

	"github.com/safing/portmaster/service/updates/helper"
)

const (
	// RestartExitCode is the exit code that any service started by portmaster-start
	// can return in order to trigger a restart after a clean shutdown.
	RestartExitCode = 23

	// ControlledFailureExitCode is the exit code that any service started by
	// portmaster-start can return in order to signify a controlled failure.
	// This disables retrying and exits with an error code.
	ControlledFailureExitCode = 24

	exeSuffix = ".exe"
	zipSuffix = ".zip"
)

var (
	runningInConsole bool
	onWindows        = runtime.GOOS == "windows"
	stdinSignals     bool
	childIsRunning   = abool.NewBool(false)
)

// Options for starting component.
type Options struct {
	Name              string
	Identifier        string // component identifier
	ShortIdentifier   string // populated automatically
	LockPathPrefix    string
	LockPerUser       bool
	PIDFile           bool
	SuppressArgs      bool // do not use any args
	AllowDownload     bool // allow download of component if it is not yet available
	AllowHidingWindow bool // allow hiding the window of the subprocess
	NoOutput          bool // do not use stdout/err if logging to file is available (did not fail to open log file)
	RestartOnFail     bool // Try restarting automatically, if the started component fails.
}

func init() {
	registerComponent([]Options{
		{
			Name:              "Portmaster Core",
			Identifier:        "core/portmaster-core",
			AllowDownload:     true,
			AllowHidingWindow: true,
			PIDFile:           true,
			RestartOnFail:     true,
		},
		{
			Name:              "Portmaster App",
			Identifier:        "app/portmaster-app.zip",
			AllowDownload:     false,
			AllowHidingWindow: false,
		},
		{
			Name:              "Portmaster Notifier",
			Identifier:        "notifier/portmaster-notifier",
			LockPerUser:       true,
			AllowDownload:     false,
			AllowHidingWindow: true,
			PIDFile:           true,
			LockPathPrefix:    "exec",
		},
		{
			Name:              "Safing Privacy Network",
			Identifier:        "hub/spn-hub",
			AllowDownload:     true,
			AllowHidingWindow: true,
			PIDFile:           true,
			RestartOnFail:     true,
		},
	})
}

func registerComponent(opts []Options) {
	for idx := range opts {
		opt := &opts[idx] // we need a copy
		if opt.ShortIdentifier == "" {
			opt.ShortIdentifier = path.Dir(opt.Identifier)
		}

		rootCmd.AddCommand(
			&cobra.Command{
				Use:   opt.ShortIdentifier,
				Short: "Run the " + opt.Name,
				RunE: func(cmd *cobra.Command, args []string) error {
					err := run(opt, args)
					initiateShutdown(err)
					return err
				},
			},
		)

		showCmd.AddCommand(
			&cobra.Command{
				Use:   opt.ShortIdentifier,
				Short: "Show command to execute the " + opt.Name,
				RunE: func(cmd *cobra.Command, args []string) error {
					return show(opt, args)
				},
			},
		)
	}
}

func getExecArgs(opts *Options, cmdArgs []string) []string {
	if opts.SuppressArgs {
		return nil
	}

	args := []string{"--data", dataDir}
	if stdinSignals {
		args = append(args, "--input-signals")
	}

	if runtime.GOOS == "linux" && opts.Identifier == "app/portmaster-app.zip" {
		// see https://www.freedesktop.org/software/systemd/man/pam_systemd.html#type=
		if xdgSessionType := os.Getenv("XDG_SESSION_TYPE"); xdgSessionType == "wayland" {
			// we're running the Portmaster UI App under Wayland so make sure we add some arguments
			// required by Electron.
			args = append(args,
				[]string{
					"--enable-features=UseOzonePlatform,WaylandWindowDecorations",
					"--ozone-platform=wayland",
				}...,
			)
		}
	}

	args = append(args, cmdArgs...)
	return args
}

func run(opts *Options, cmdArgs []string) (err error) {
	// set download option
	registry.Online = opts.AllowDownload

	if isShuttingDown() {
		return nil
	}

	// get original arguments
	// additional parameters can be specified using -- --some-parameter
	args := getExecArgs(opts, cmdArgs)

	// check for duplicate instances
	if opts.PIDFile {
		pid, err := checkAndCreateInstanceLock(opts.LockPathPrefix, opts.ShortIdentifier, opts.LockPerUser)
		if err != nil {
			return fmt.Errorf("failed to exec lock: %w", err)
		}
		if pid != 0 {
			return fmt.Errorf("another instance of %s is already running: PID %d", opts.Name, pid)
		}
		defer func() {
			err := deleteInstanceLock(opts.LockPathPrefix, opts.ShortIdentifier, opts.LockPerUser)
			if err != nil {
				log.Printf("failed to delete instance lock: %s\n", err)
			}
		}()
	}

	// notify service after some time
	go func() {
		// assume that after 3 seconds service has finished starting
		time.Sleep(3 * time.Second)
		startupComplete <- struct{}{}
	}()

	// adapt identifier
	if onWindows && !strings.HasSuffix(opts.Identifier, zipSuffix) {
		opts.Identifier += exeSuffix
	}

	// setup logging
	// init log file
	logFile := getPmStartLogFile(".log")
	if logFile != nil {
		// don't close logFile, will be closed by system
		if opts.NoOutput {
			log.Println("disabling log output to stdout... bye!")
			log.SetOutput(logFile)
		} else {
			log.SetOutput(io.MultiWriter(os.Stdout, logFile))
		}
	}

	return runAndRestart(opts, args)
}

func runAndRestart(opts *Options, args []string) error {
	tries := 0
	for {
		tryAgain, err := execute(opts, args)
		if err != nil {
			log.Printf("%s failed with: %s\n", opts.Identifier, err)
			tries++
			if tries >= maxRetries {
				log.Printf("encountered %d consecutive errors, giving up ...", tries)
				return err
			}
		} else {
			tries = 0
			log.Printf("%s exited without error", opts.Identifier)
		}

		if !opts.RestartOnFail || !tryAgain {
			return err
		}

		// if a restart was requested `tries` is set to 0 so
		// this becomes a no-op.
		time.Sleep(time.Duration(2*tries) * time.Second)

		if tries >= 2 || err == nil {
			// if we are constantly failing or a restart was requested
			// try to update the resources.
			log.Printf("updating registry index")
			_ = updateRegistryIndex(false) // will always return nil
		}
	}
}

func fixExecPerm(path string) error {
	if onWindows {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if info.Mode() == 0o0755 {
		return nil
	}

	if err := os.Chmod(path, 0o0755); err != nil { //nolint:gosec // Set execution rights.
		return fmt.Errorf("failed to chmod %s: %w", path, err)
	}

	return nil
}

func copyLogs(opts *Options, consoleSink io.Writer, version, ext string, logSource io.Reader, notifier chan<- struct{}) {
	defer func() { notifier <- struct{}{} }()

	sink := consoleSink

	fileSink := getLogFile(opts, version, ext)
	if fileSink != nil {
		defer finalizeLogFile(fileSink)
		if opts.NoOutput {
			sink = fileSink
		} else {
			sink = io.MultiWriter(consoleSink, fileSink)
		}
	}

	if bytes, err := io.Copy(sink, logSource); err != nil {
		log.Printf("%s: writing logs failed after %d bytes: %s", fileSink.Name(), bytes, err)
	}
}

func persistOutputStreams(opts *Options, version string, cmd *exec.Cmd) (chan struct{}, error) {
	var (
		done         = make(chan struct{})
		copyNotifier = make(chan struct{}, 2)
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to connect stdout: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to connect stderr: %w", err)
	}

	go copyLogs(opts, os.Stdout, version, ".log", stdout, copyNotifier)
	go copyLogs(opts, os.Stderr, version, ".error.log", stderr, copyNotifier)

	go func() {
		<-copyNotifier
		<-copyNotifier
		close(copyNotifier)
		close(done)
	}()

	return done, nil
}

func execute(opts *Options, args []string) (cont bool, err error) {
	file, err := registry.GetFile(
		helper.PlatformIdentifier(opts.Identifier),
	)
	if err != nil {
		return true, fmt.Errorf("could not get component: %w", err)
	}
	binPath := file.Path()

	// Adapt path for packaged software.
	if strings.HasSuffix(binPath, zipSuffix) {
		// Remove suffix from binary path.
		binPath = strings.TrimSuffix(binPath, zipSuffix)
		// Add binary with the same name to access the unpacked binary.
		binPath = filepath.Join(binPath, filepath.Base(binPath))

		// Adapt binary path on Windows.
		if onWindows {
			binPath += exeSuffix
		}
	}

	// check permission
	if err := fixExecPerm(binPath); err != nil {
		return true, err
	}

	log.Printf("starting %s %s\n", binPath, strings.Join(args, " "))

	// create command
	exc := exec.Command(binPath, args...)

	if !runningInConsole && opts.AllowHidingWindow {
		// Windows only:
		// only hide (all) windows of program if we are not running in console and windows may be hidden
		hideWindow(exc)
	}

	outputsWritten, err := persistOutputStreams(opts, file.Version(), exc)
	if err != nil {
		return true, err
	}

	interrupt, err := getProcessSignalFunc(exc)
	if err != nil {
		return true, err
	}

	err = exc.Start()
	if err != nil {
		return true, fmt.Errorf("failed to start %s: %w", opts.Identifier, err)
	}
	childIsRunning.Set()

	// wait for completion
	finished := make(chan error, 1)
	go func() {
		defer close(finished)

		<-outputsWritten
		// wait for process to return
		finished <- exc.Wait()
		// update status
		childIsRunning.UnSet()
	}()

	// state change listeners
	select {
	case <-shuttingDown:
		if err := interrupt(); err != nil {
			log.Printf("failed to signal %s to shutdown: %s\n", opts.Identifier, err)
			err = exc.Process.Kill()
			if err != nil {
				return false, fmt.Errorf("failed to kill %s: %w", opts.Identifier, err)
			}
			return false, fmt.Errorf("killed %s", opts.Identifier)
		}

		// wait until shut down
		select {
		case <-finished:
		case <-time.After(3 * time.Minute): // portmaster core prints stack if not able to shutdown in 3 minutes, give it one more ...
			err = exc.Process.Kill()
			if err != nil {
				return false, fmt.Errorf("failed to kill %s: %w", opts.Identifier, err)
			}
			return false, fmt.Errorf("killed %s", opts.Identifier)
		}
		return false, nil

	case err := <-finished:
		return parseExitError(err)
	}
}

func getProcessSignalFunc(cmd *exec.Cmd) (func() error, error) {
	if stdinSignals {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to connect stdin: %w", err)
		}

		return func() error {
			_, err := fmt.Fprintln(stdin, "SIGINT")
			return err
		}, nil
	}

	return func() error {
		return cmd.Process.Signal(os.Interrupt)
	}, nil
}

func parseExitError(err error) (restart bool, errWithCtx error) {
	if err == nil {
		// clean and coordinated exit
		return false, nil
	}

	var exErr *exec.ExitError
	if errors.As(err, &exErr) {
		switch exErr.ProcessState.ExitCode() {
		case 0:
			return false, fmt.Errorf("clean exit with error: %w", err)
		case 1:
			return true, fmt.Errorf("error during execution: %w", err)
		case RestartExitCode:
			return true, nil
		case ControlledFailureExitCode:
			return false, errors.New("controlled failure, check logs")
		default:
			return true, fmt.Errorf("unknown exit code %w", exErr)
		}
	}

	return true, fmt.Errorf("unexpected error type: %w", err)
}
