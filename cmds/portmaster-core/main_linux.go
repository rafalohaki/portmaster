package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	processInfo "github.com/shirou/gopsutil/process"

	"github.com/safing/portmaster/base/log"
	"github.com/safing/portmaster/service"
)

func run(instance *service.Instance) {
	// Set default log level.
	_ = log.Start(log.WarningLevel)

	// Start
	go func() {
		err := instance.Start()
		if err != nil {
			fmt.Printf("instance start failed: %s\n", err)

			// Print stack on start failure, if enabled.
			if printStackOnExit {
				printStackTo(os.Stdout, "PRINTING STACK ON START FAILURE")
			}

			os.Exit(1)
		}
	}()

	// Wait for signal.
	signalCh := make(chan os.Signal, 1)
	if enableInputSignals {
		go inputSignals(signalCh)
	}
	signal.Notify(
		signalCh,
		os.Interrupt,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		sigUSR1,
	)

	select {
	case sig := <-signalCh:
		// Only print and continue to wait if SIGUSR1
		if sig == sigUSR1 {
			printStackTo(os.Stderr, "PRINTING STACK ON REQUEST")
		} else {
			fmt.Println(" <INTERRUPT>") // CLI output.
			slog.Warn("program was interrupted, stopping")
		}

	case <-instance.Stopped():
		log.Shutdown()
		os.Exit(instance.ExitCode())
	}

	// Catch signals during shutdown.
	// Rapid unplanned disassembly after 5 interrupts.
	go func() {
		forceCnt := 5
		for {
			<-signalCh
			forceCnt--
			if forceCnt > 0 {
				fmt.Printf(" <INTERRUPT> again, but already shutting down - %d more to force\n", forceCnt)
			} else {
				printStackTo(os.Stderr, "PRINTING STACK ON FORCED EXIT")
				os.Exit(1)
			}
		}
	}()

	// Rapid unplanned disassembly after 3 minutes.
	go func() {
		time.Sleep(3 * time.Minute)
		printStackTo(os.Stderr, "PRINTING STACK - TAKING TOO LONG FOR SHUTDOWN")
		os.Exit(1)
	}()

	// Stop instance.
	if err := instance.Stop(); err != nil {
		slog.Error("failed to stop", "err", err)
	}
	log.Shutdown()

	// Print stack on shutdown, if enabled.
	if printStackOnExit {
		printStackTo(os.Stdout, "PRINTING STACK ON EXIT")
	}

	// Check if restart was trigger and send start service command if true.
	if isRunningAsService() && instance.ShouldRestart {
		_ = runServiceRestart()
	}

	os.Exit(instance.ExitCode())
}

func runServiceRestart() error {
	// Check if user defined custom command for restarting the service.
	restartCommand, exists := os.LookupEnv("PORTMASTER_RESTART_COMMAND")

	// Run the service restart
	var cmd *exec.Cmd
	if exists && restartCommand != "" {
		log.Debugf(`instance: running custom restart command: "%s"`, restartCommand)
		cmd = exec.Command("sh", "-c", restartCommand)
	} else {
		cmd = exec.Command("systemctl", "restart", "portmaster")
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed run restart command: %w", err)
	}
	return nil
}

func isRunningAsService() bool {
	// Get the current process ID
	pid := os.Getpid()

	currentProcess, err := processInfo.NewProcess(int32(pid))
	if err != nil {
		return false
	}

	ppid, err := currentProcess.Ppid()
	if err != nil {
		return false
	}
	// Check if the parent process ID is 1 == init system
	return ppid == 1
}

func platformSpecificChecks() {
	// If flag is set. Run recover IP tables and exit. (Can be true only on linux)
	if recoverIPTables {
		exitCode := 0
		err := recoverIPTablesCmd()
		if err != nil {
			fmt.Printf("failed: %s", err)
			exitCode = 1
		}

		os.Exit(exitCode)
	}
}
