package main

import (
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

	commonOptions := "rw,nosuid,relatime"

	// Mount /proc
	if err := mount("proc", "/proc", "proc", commonOptions+",nodev,noexec", false); err != nil {
		panic(err)
	}

	// Mount /sys
	if err := mount("sys", "/sys", "sysfs", commonOptions+",nodev,noexec", false); err != nil {
		panic(err)
	}

	// Mount /dev
	if err := mount("dev", "/dev", "devtmpfs", commonOptions+",mode=755,inode64", false); err != nil {
		panic(err)
	}

	// Mount /run
	if err := mount("run", "/run", "tmpfs", commonOptions+",nodev,mode=755,inode64", false); err != nil {
		panic(err)
	}

	// Mount /dev/pts
	if err := mount("devpts", "/dev/pts", "devpts", commonOptions+",gid=5,mode=620,ptmxmode=000", true); err != nil {
		panic(err)
	}

	// Mount /dev/shm
	if err := mount("shm", "/dev/shm", "tmpfs", commonOptions+",nodev,inode64", true); err != nil {
		panic(err)
	}

	// Mount securityfs
	if err := mount("securityfs", "/sys/kernel/security", "securityfs", commonOptions, false); err != nil {
		panic(err)
	}

	// Mount cgroups v2
	if err := mount("cgroup2", "/sys/fs/cgroup", "cgroup2", commonOptions+",noexec,nsdelegate,memory_recursiveprot", false); err != nil {
		panic(err)
	}

	fmt.Println("Done.")
}

func mountFilesystems() {
	fmt.Print("Mounting fstab entries... ")

	if err := mountFstabEntries(); err != nil {
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

	process, _ := os.FindProcess(serviceManagerPid)

	// Send SIGTERM signal to service manager
	if err := process.Signal(syscall.SIGTERM); err != nil {
		log.Println("Could not stop service manager!")
		syscall.Kill(serviceManagerPid, syscall.SIGKILL)
		return
	}

	// Check if service manager has stopped gracefully, otherwise send sigkill on timeout
	exited := make(chan bool)
	go func() {
		for {
			if err := process.Signal(syscall.Signal(0)); err != nil {
				break
			}
		}
		exited <- true
	}()

	for {
		select {
		case <-exited:
			fmt.Println("Done.")
			return
		case <-time.After(60 * time.Second):
			log.Println("Could not stop service manager!")
			syscall.Kill(serviceManagerPid, syscall.SIGKILL)
			return
		default:
			waitZombieProcesses()
		}
	}

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
	for {
		switch <-sigc {
		case syscall.SIGUSR1:
			signal.Stop(sigc)
			shutdownSystem()
		case syscall.SIGTERM, syscall.SIGINT:
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
