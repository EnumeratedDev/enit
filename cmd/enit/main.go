package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Build-time variables
var version = "dev"
var sysconfdir = "/etc/"
var runstatedir = "/var/run/"

var serviceManagerPid int

func main() {
	// Set Process Name
	err := setProcessName()
	if err != nil {
		log.Printf("Could not set process name! Error: %s", err)
	}

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
	// Set hostname
	setHostname()
	// Start service manager
	startServiceManager()

	// Run function once to wait zombie processes created by initcpio
	waitZombieProcesses()

	fmt.Println()

	// Catch signals
	catchSignals()
}

func setProcessName() error {
	bytes := append([]byte("enit"), 0)
	ptr := unsafe.Pointer(&bytes[0])
	if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, syscall.PR_SET_NAME, uintptr(ptr), 0, 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

func mountVirtualFilesystems() {
	fmt.Print("Mounting virtual filesystems... ")

	commonFlags := uintptr(0 | syscall.MS_NOSUID | syscall.MS_RELATIME)
	// Mount /proc
	if err := syscall.Mount("proc", "/proc", "proc", commonFlags|syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_REMOUNT, ""); err != nil {
		panic(err)
	}
	// Mount /sys
	if err := syscall.Mount("sys", "/sys", "sysfs", commonFlags|syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_REMOUNT, ""); err != nil {
		panic(err)
	}
	// Mount /dev
	if err := syscall.Mount("dev", "/dev", "devtmpfs", commonFlags|syscall.MS_REMOUNT, "mode=755,inode64"); err != nil {
		panic(err)
	}
	// Mount /run
	if err := syscall.Mount("run", "/run", "tmpfs", commonFlags|syscall.MS_NODEV|syscall.MS_REMOUNT, "mode=755,inode64"); err != nil {
		panic(err)
	}
	// Mount /dev/pts
	if err := os.Mkdir("/dev/pts", 0755); err != nil && !errors.Is(err, os.ErrExist) {
		panic(err)
	}
	if err := syscall.Mount("devpts", "/dev/pts", "devpts", commonFlags, "gid=5,mode=620,ptmxmode=000"); err != nil {
		panic(err)
	}
	// Mount /dev/shm
	if err := os.Mkdir("/dev/shm", 0755); err != nil && !errors.Is(err, os.ErrExist) {
		panic(err)
	}
	if err := syscall.Mount("shm", "/dev/shm", "tmpfs", commonFlags|syscall.MS_NODEV, "inode64"); err != nil {
		panic(err)
	}
	// Mount securityfs
	if err := syscall.Mount("securityfs", "/sys/kernel/security", "securityfs", commonFlags, ""); err != nil {
		panic(err)
	}
	// Mount cgroups v2
	if err := syscall.Mount("cgroup2", "/sys/fs/cgroup", "cgroup2", commonFlags|syscall.MS_NOEXEC, ""); err != nil {
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
	exit := false
	for timeout := time.After(60 * time.Second); ; {
		if exit {
			break
		}
		select {
		case <-timeout:
			log.Println("Could not stop service manager!")
			err := syscall.Kill(serviceManagerPid, syscall.SIGKILL)
			if err != nil {
				log.Println("Could not stop service manager!")
			}
			exit = true
		default:
			waitZombieProcesses()
			p, err := os.FindProcess(serviceManagerPid)
			if err != nil {
				exit = true
				break
			}
			err = p.Signal(syscall.Signal(0))
			if err != nil {
				exit = true
			}
		}
	}

	fmt.Println("Done.")
}

func setHostname() {
	fmt.Print("Setting hostname... ")

	bytes, err := os.ReadFile("/etc/hostname")
	if err != nil {
		log.Println("Could not set hostname!")
		return
	}

	hostname := strings.TrimSpace(string(bytes))

	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		log.Println("Could not set hostname!")
		return
	}

	fmt.Println("Done.")
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
