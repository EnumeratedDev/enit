package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"syscall"
	"time"
)

// Build-time variables
var version = "dev"
var sysconfdir = "/etc/"
var runstatedir = "/var/run/"

var serviceManagerPid int

func main() {
	// Parse flags
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *printVersion {
		fmt.Printf("Enit version %s\n", version)
		os.Exit(0)
	}

	if os.Getpid() != 1 {
		fmt.Println("Enit must be run as PID 1!")
		os.Exit(1)
	}

	fmt.Println("Starting Enit...")

	// Mount virtual filesystems
	mountVirtualFilesystems()
	// Mount filesystems in fstab
	mountFilesystems()
	// Start service manager
	startServiceManager()

	// Run function once to wait zombie processes created by initcpio
	waitZombieProcesses()

	fmt.Println()

	// Catch signals
	catchSignals()
}

func mountVirtualFilesystems() {
	fmt.Print("Mounting virtual filesystems... ")

	if err := os.Mkdir("/dev/pts", 0755); err != nil {
		panic(err)
	}
	if err := syscall.Mount("none", "/dev/pts", "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, ""); err != nil {
		panic(err)
	}

	fmt.Println("Done.")
}

func mountFilesystems() {
	fmt.Print("Mounting filesystems... ")

	cmd := exec.Command("/bin/mount", "-a")
	err := cmd.Run()

	if err != nil {
		log.Println("Could not mount fstab entries!")
		panic(err)
	}

	fmt.Println("Done.")
}

func startServiceManager() {
	fmt.Print("Initializing service manager... ")

	cmd := exec.Command("/sbin/esvm", path.Join(runstatedir, "esvm"), path.Join(sysconfdir, "esvm"))
	err := cmd.Start()
	if err != nil {
		log.Println("Could not initialize service manager!")
		panic(err)
	}
	serviceManagerPid = cmd.Process.Pid

	fmt.Println("Done")
}

func stopServiceManager() {
	fmt.Println("Stopping service manager... ")

	err := syscall.Kill(serviceManagerPid, syscall.SIGTERM)
	if err != nil {
		log.Println("Could not stop service manager!")
	}
	// Check if service manager has stopped gracefully, otherwise send sigkill on timeout
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		log.Println("Could not stop service manager!")
		err := syscall.Kill(serviceManagerPid, syscall.SIGKILL)
		if err != nil {
			log.Println("Could not stop service manager!")
		}
	case <-ticker.C:
		p, err := os.FindProcess(serviceManagerPid)
		if err != nil {
			break
		}
		err = p.Signal(syscall.Signal(0))
		if err != nil {
			break
		}
	}

	fmt.Print("Done.")
}

func waitZombieProcesses() {
	for {
		if wpid, _ := syscall.Wait4(-1, nil, syscall.WNOHANG, nil); wpid <= 0 {
			break
		}
	}
}

func catchSignals() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGUSR1, syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD)
	defer close(sigc)
	defer signal.Stop(sigc)
	for {
		switch <-sigc {
		case syscall.SIGUSR1:
			close(sigc)
			signal.Stop(sigc)
			shutdownSystem()
		case syscall.SIGTERM, syscall.SIGINT:
			close(sigc)
			signal.Stop(sigc)
			rebootSystem()
		case syscall.SIGCHLD:
			waitZombieProcesses()
		}
	}
}

func shutdownSystem() {
	fmt.Println("Shutting down...")

	stopServiceManager()

	fmt.Println("Syncing disks...")
	syscall.Sync()

	fmt.Println("Sending shutdown syscall...")
	err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	if err != nil {
		panic(err)
	}
}

func rebootSystem() {
	fmt.Println("Rebooting...")

	stopServiceManager()

	fmt.Println("Syncing disks...")
	syscall.Sync()

	fmt.Println("Sending reboot syscall...")
	err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	if err != nil {
		panic(err)
	}
}
