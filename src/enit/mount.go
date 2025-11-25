package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

var flagsEquivalence = map[string]uintptr{
	"dirsync":     unix.MS_DIRSYNC,
	"lazytime":    unix.MS_LAZYTIME,
	"noatime":     unix.MS_NOATIME,
	"nodev":       unix.MS_NODEV,
	"nodiratime":  unix.MS_NODIRATIME,
	"noexec":      unix.MS_NOEXEC,
	"nosuid":      unix.MS_NOSUID,
	"ro":          unix.MS_RDONLY,
	"rw":          0,
	"relatime":    unix.MS_RELATIME,
	"silent":      unix.MS_SILENT,
	"strictatime": unix.MS_STRICTATIME,
	"sync":        unix.MS_SYNCHRONOUS,
	"defaults":    0,
}

// Split string flags to mount flags and mount data
func convertMountOptions(options string) (flags []uintptr, data string, extra []string) {
	for _, flag := range strings.Split(options, ",") {
		if unixFlag, ok := flagsEquivalence[flag]; ok {
			flags = append(flags, unixFlag)
		} else {
			if flag == "noauto" || flag == "nofail" {
				extra = append(extra, flag)
			} else if data == "" {
				data = flag
			} else {
				data += "," + flag
			}
		}
	}

	return flags, data, extra
}

// Combine a unix flag slice or array into a single uintptr
func combineUnixFlags(flagsSlice []uintptr) (flags uintptr) {
	flags = 0
	for _, flag := range flagsSlice {
		flags |= flag
	}

	return flags
}

// Check whether a certain path is a mountpoint
func isMountpoint(mountpoint string) bool {
	if mountpoint != "/" {
		mountpoint = strings.TrimRight(mountpoint, "/")
	}

	if _, err := os.Stat("/proc/mounts"); err != nil {
		return false
	}

	bytes, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.Split(line, " ")[1] == mountpoint {
			return true
		}
	}

	return false
}

func mount(source, target, fstype string, options string, mkdir bool) error {
	flags, data, _ := convertMountOptions(options)

	if isMountpoint(target) && !slices.Contains(flags, unix.MS_REMOUNT) {
		flags = append(flags, unix.MS_REMOUNT)
	}

	if mkdir {
		err := os.MkdirAll(target, 0755)
		if err != nil {
			return err
		}
	}

	if err := unix.Mount(source, target, fstype, combineUnixFlags(flags), data); err != nil {
		return err
	}

	return nil
}

func mountFstabEntries() (error, int) {
	if _, err := os.Stat("/etc/fstab"); os.IsNotExist(err) {
		return nil, 0
	} else if err != nil {
		return err, 0
	}

	bytes, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return err, 0
	}

	swapPriority := -2

	for i, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Get fields from line
		fields := []string{}
		sb := &strings.Builder{}
		quoted := false
		for _, r := range line {
			if r == '"' {
				quoted = !quoted
			} else if !quoted && r == ' ' {
				str := sb.String()
				if len(strings.TrimSpace(str)) > 0 {
					fields = append(fields, sb.String())
				}
				sb.Reset()
			} else {
				sb.WriteRune(r)
			}
		}
		if sb.Len() > 0 {
			fields = append(fields, sb.String())
		}
		if len(fields) < 4 {
			return fmt.Errorf("Not enough fields"), i + 1
		}
		source := fields[0]
		target := fields[1]
		fstype := fields[2]
		options := fields[3]

		// Replace device prefixes
		if cutSource, ok := strings.CutPrefix(source, "LABEL="); ok {
			source = "/dev/disk/by-label/" + strings.ReplaceAll(cutSource, " ", "\\x20")
		} else if cutSource, ok := strings.CutPrefix(source, "UUID="); ok {
			source = "/dev/disk/by-uuid/" + cutSource
		} else if cutSource, ok := strings.CutPrefix(source, "PARTLABEL="); ok {
			source = "/dev/disk/by-partlabel/" + cutSource
		} else if cutSource, ok := strings.CutPrefix(source, "PARTUUID="); ok {
			source = "/dev/disk/by-partuuid/" + cutSource
		}

		flags, data, extra := convertMountOptions(options)

		if slices.Contains(extra, "noauto") {
			continue
		}

		if fstype == "swap" {
			b := append([]byte(source), 0)
			const SwapFlagPrioShift = 0
			const SwapFlagPrioMask = 0x7fff
			_, _, err := unix.Syscall(unix.SYS_SWAPON, uintptr(unsafe.Pointer(&b[0])), uintptr((swapPriority<<SwapFlagPrioShift)&SwapFlagPrioMask), 0)
			swapPriority--
			if err != 0 {
				if slices.Contains(extra, "nofail") {
					fmt.Printf("Warning: could not mount fstab entry on line %d: swapon syscall returned non-zero exit code: %d\n", i+1, err)
				} else {
					return fmt.Errorf("swapon syscall returned non-zero exit code: %d", err), i + 1
				}
			}
			continue
		}

		if isMountpoint(target) && !slices.Contains(flags, unix.MS_REMOUNT) {
			flags = append(flags, unix.MS_REMOUNT)
		}

		if err := unix.Mount(source, target, fstype, combineUnixFlags(flags), data); err != nil {
			if slices.Contains(extra, "nofail") {
				log.Printf("Warning: could not mount fstab entry on line %d: %s\n", i+1, err)
			} else {
				return err, i + 1
			}
		}
	}

	return nil, 0
}

func unmountFilesystems() {
	// Disable all swap memory
	data, err := os.ReadFile("/proc/swaps")
	if err != nil {
		log.Fatal(err)
	}

	for i, entry := range strings.Split(string(data), "\n") {
		if i == 0 {
			continue
		}
		entry = strings.TrimSpace(entry)
		if len(entry) == 0 {
			continue
		}

		mountpoint := strings.Fields(entry)[0]

		// Unmount swap at mountpoint
		fmt.Printf("Disabling swap at %s... ", mountpoint)
		b := append([]byte(mountpoint), 0)
		_, _, err := unix.Syscall(unix.SYS_SWAPOFF, uintptr(unsafe.Pointer(&b[0])), 0, 0)
		if err == 0 {
			fmt.Println("Done.")
		} else {
			fmt.Printf("Error: %s\n", err.Error())
		}
	}

	data, err = os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		log.Fatal(err)
	}

	// Unmount filesystems
	entries := strings.Split(string(data), "\n")
	slices.Reverse(entries)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if len(entry) == 0 {
			continue
		}

		// Get entry fields
		fields := strings.Fields(entry)
		mountpoint := fields[4]
		filesystem := ""
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				filesystem = fields[i+1]
				break
			}
		}

		// Skip root filesystem
		if mountpoint == "/" {
			continue
		}

		// Skip root and ignored filesystems
		ignoredFilesystems := []string{
			"devtmpfs",
			"proc",
			"sysfs",
			"tmpfs",
		}

		if slices.Contains(ignoredFilesystems, filesystem) {
			continue
		}

		// Unmount filesystem at mountpoint
		fmt.Printf("Unmounting %s...", mountpoint)
		err := unix.Unmount(mountpoint, 0)
		if errors.Is(err, syscall.EBUSY) {
			fmt.Println(" Busy.")
			time.Sleep(1 * time.Second)
		} else if err != nil {
			fmt.Printf(" Error: %s\n", err.Error())
		} else {
			fmt.Println(" Done.")
		}
	}
}

func remountRootReadonly() {
	fmt.Print("Remounting root as read-only...")

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		log.Fatal(err)
	}

	filesystem := ""
	source := ""
	fsData := ""

	// Get root filesystems
	entries := strings.Split(string(data), "\n")
	slices.Reverse(entries)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if len(entry) == 0 {
			continue
		}

		// Get entry fields
		fields := strings.Fields(entry)
		mountpoint := fields[4]
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				filesystem = fields[i+1]
				source = fields[i+2]
				fsData = fields[i+3]
				break
			}
		}

		if mountpoint == "/" {
			break
		}
	}

	err = unix.Mount(source, "/", filesystem, syscall.MS_RDONLY|syscall.MS_REMOUNT, fsData)
	if errors.Is(err, syscall.EBUSY) {
		fmt.Println(" Busy.")
	} else if err != nil {
		fmt.Printf(" Error: %s\n", err.Error())
	} else {
		fmt.Println(" Done.")
	}
}
