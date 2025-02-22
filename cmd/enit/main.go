package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strconv"
	"syscall"
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
	startTerminal()

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

func waitZombieProcesses() {
	for {
		if wpid, _ := syscall.Wait4(-1, nil, syscall.WNOHANG, nil); wpid <= 0 {
			break
		}
	}
}

func startTerminal() {
	for i := 1; i < 6; i++ {
		cmd := exec.Command("/sbin/agetty", "--noclear", "tty"+strconv.Itoa(i))
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Start()

		if err != nil {
			log.Println("Could not start agetty terminal on tty" + strconv.Itoa(i) + "!")
			panic(err)
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

	fmt.Println("Stopping services... ")
	err := syscall.Kill(serviceManagerPid, syscall.SIGTERM)
	if err != nil {
		log.Println("Could not stop service manager!")
		panic(err)
	}
	fmt.Print("Done.")

	fmt.Println("Sending shutdown syscall...")
	syscall.Sync()
	err = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	if err != nil {
		panic(err)
	}
}

func rebootSystem() {
	fmt.Println("Rebooting...")

	fmt.Println("Stopping service manager... ")
	err := syscall.Kill(serviceManagerPid, syscall.SIGTERM)
	if err != nil {
		log.Println("Could not stop service manager!")
		panic(err)
	}
	fmt.Print("Done.")

	fmt.Println("Sending reboot syscall...")
	syscall.Sync()
	err = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	if err != nil {
		panic(err)
	}
}
