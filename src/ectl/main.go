package main

import (
	"fmt"
	"log"
	"os"
	"syscall"
)

// Build-time variables
var version = "dev"
var sysconfdir = "/etc/"
var runstatedir = "/var/run/"

func main() {

	// Show usage if no arguments specified
	if len(os.Args) == 1 {
		printUsage()
		return
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "v", "version":
		fmt.Printf("Enit Control version %s\n", version)
	case "shutdown", "poweroff", "halt":
		err := syscall.Kill(1, syscall.SIGUSR1)
		if err != nil {
			log.Fatalf("Could not send shutdown signal! Error: %s\n", err)
		}
	case "reboot", "restart", "reset":
		err := syscall.Kill(1, syscall.SIGTERM)
		if err != nil {
			log.Fatalf("Could not send reboot signal! Error: %s\n", err)
		}
	case "sv", "service":
		handleServiceSubcommand()
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: ectl <subcommand> [options]")
	fmt.Println("Description: Shutdown, reboot and manage system services")
	fmt.Println("Sucommands:")
	fmt.Println("  v, version                 Show enit version")
	fmt.Println("  shutdown, poweroff, halt   Shutdown the system")
	fmt.Println("  reboot, restart, reset     Reboot the system")
	fmt.Println("  sv, service                Manage system services")
}
