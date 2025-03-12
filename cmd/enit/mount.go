package main

import (
	"golang.org/x/sys/unix"
	"os"
	"slices"
	"strings"
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
func convertMountOptions(options string) (flags []uintptr, data string) {
	for _, flag := range strings.Split(options, ",") {
		if unixFlag, ok := flagsEquivalence[flag]; ok {
			flags = append(flags, unixFlag)
		} else {
			if data == "" {
				data = flag
			} else {
				data += "," + flag
			}
		}
	}

	return flags, data
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
	flags, data := convertMountOptions(options)

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

func mountFstabEntries() error {
	if _, err := os.Stat("/etc/fstab"); err != nil {
		return err
	}

	bytes, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return err
	}

	for _, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		source := strings.Split(line, " ")[0]
		target := strings.Split(line, " ")[1]
		fstype := strings.Split(line, " ")[2]
		options := strings.Split(line, " ")[3]

		flags, data := convertMountOptions(options)

		if slices.Contains(strings.Split(data, ","), "noauto") {
			continue
		}

		if isMountpoint(target) && !slices.Contains(flags, unix.MS_REMOUNT) {
			flags = append(flags, unix.MS_REMOUNT)
		}

		if err := unix.Mount(source, target, fstype, combineUnixFlags(flags), data); err != nil {
			return err
		}
	}

	return nil
}
