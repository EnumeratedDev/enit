package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Build-time variables
var version = "dev"
var sysconfdir = "/etc/"
var runstatedir = "/var/run/"

var socket net.Conn

func main() {

	// Set and parse flags
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	// Dial esvm socket
	dialSocket()
	defer socket.Close()

	if flag.NArg() < 1 {
		printUsage()
		os.Exit(1)
	}

	if *printVersion || flag.Args()[0] == "version" {
		fmt.Printf("Enit Control version %s\n", version)
		return
	} else if flag.Args()[0] == "help" {
		printUsage()
		return
	} else if flag.Args()[0] == "shutdown" || flag.Args()[0] == "poweroff" || flag.Args()[0] == "halt" {
		err := syscall.Kill(1, syscall.SIGUSR1)
		if err != nil {
			log.Fatalf("Could not send shutdown signal! Error: %s\n", err)
		}
		return
	} else if flag.Args()[0] == "reboot" || flag.Args()[0] == "restart" || flag.Args()[0] == "reset" {
		err := syscall.Kill(1, syscall.SIGTERM)
		if err != nil {
			log.Fatalf("Could not send shutdown signal! Error: %s\n", err)
		}
		return
	} else if flag.Args()[0] == "service" || flag.Args()[0] == "sv" {
		if len(flag.Args()) <= 1 {
			fmt.Println("Usage: ectl service <start/stop/enable/disable/status/list> [service]")
			return
		} else if flag.Args()[1] == "list" {
			fmt.Println("list")
			return
		} else if len(flag.Args()) <= 2 {
			fmt.Printf("Usage: ectl service %s <service>\n", flag.Args()[1])
			return
		} else if flag.Args()[1] == "start" {
			flag.Args()[2] = strings.TrimSuffix(flag.Args()[2], ".esv")
			if _, err := os.Stat(path.Join(sysconfdir, "esvm/services/", flag.Args()[2]+".esv")); err != nil {
				log.Fatalf("Could not start service! Error: %s\n", err)
			}

			_, err := socket.Write([]byte("start " + flag.Args()[2]))
			if err != nil {
				log.Fatalf("Could not start service! Error: %s\n", err)
			}

			buf := make([]byte, 1024)
			n, err := socket.Read(buf)
			if err != nil {
				log.Fatalf("Could not start service! Error: %s\n", err)
			}
			if string(buf[:n]) != "ok" {
				log.Fatalf("Could not start service! Error: expcted 'ok' got '%s'\n", string(buf))
			}

			fmt.Println("Service started successfully!")
			return
		} else if flag.Args()[1] == "stop" {
			flag.Args()[2] = strings.TrimSuffix(flag.Args()[2], ".esv")
			if _, err := os.Stat(path.Join(sysconfdir, "esvm/services/", flag.Args()[2]+".esv")); err != nil {
				log.Fatalf("Could not stop service! Error: %s\n", err)
			}

			_, err := socket.Write([]byte("stop " + flag.Args()[2]))
			if err != nil {
				log.Fatalf("Could not stop service! Error: %s\n", err)
			}

			buf := make([]byte, 1024)
			n, err := socket.Read(buf)
			if err != nil {
				log.Fatalf("Could not stop service! Error: %s\n", err)
			}
			if string(buf[:n]) != "ok" {
				log.Fatalf("Could not stop service! Error: expcted 'ok' got '%s'\n", string(buf))
			}
			fmt.Println("Service stopped successfully!")
			return
		} else if flag.Args()[1] == "restart" || flag.Args()[1] == "reload" {
			flag.Args()[2] = strings.TrimSuffix(flag.Args()[2], ".esv")
			if _, err := os.Stat(path.Join(sysconfdir, "esvm/services/", flag.Args()[2]+".esv")); err != nil {
				log.Fatalf("Could not stop service! Error: %s\n", err)
			}

			_, err := socket.Write([]byte("restart " + flag.Args()[2]))
			if err != nil {
				log.Fatalf("Could not restart service! Error: %s\n", err)
			}

			buf := make([]byte, 1024)
			n, err := socket.Read(buf)
			if err != nil {
				log.Fatalf("Could not restart service! Error: %s\n", err)
			}
			if string(buf[:n]) != "ok" {
				log.Fatalf("Could not restart service! Error: expcted 'ok' got '%s'\n", string(buf))
			}
			fmt.Println("Service restarted successfully!")
			return
		} else if flag.Args()[1] == "status" {
			flag.Args()[2] = strings.TrimSuffix(flag.Args()[2], ".esv")
			if _, err := os.Stat(path.Join(sysconfdir, "esvm/services/", flag.Args()[2]+".esv")); err != nil {
				log.Fatalf("Could not stop service! Error: %s\n", err)
			}

			var state uint64
			bytes, err := os.ReadFile(path.Join(runstatedir, "esvm", flag.Args()[2], "state"))
			if err != nil {
				state = 0
			}
			state, err = strconv.ParseUint(string(bytes), 10, 8)

			fmt.Println("Service name: " + flag.Args()[2])
			switch state {
			case 0:
				fmt.Println("Service state: Unknown")
			case 1:
				fmt.Println("Service state: Unloaded")
			case 2:
				fmt.Println("Service state: Running")
			case 3:
				fmt.Println("Service state: Stopped")
			case 4:
				fmt.Println("Service state: Crashed")
			}
			return
		}
	}

	printUsage()
	os.Exit(1)
}

func printUsage() {
	fmt.Println("Available sucommands:")
	fmt.Println("ectl version | Show enit version")
	fmt.Println("ectl shutdown/poweroff/halt | Shutdown the system")
	fmt.Println("ectl reboot/restart | Reboot the system")
	fmt.Println("ectl help | Show command explanations")
	fmt.Println("ectl sv/service start <service> | Start a service")
	fmt.Println("ectl sv/service stop <service> | Stop a service")
	fmt.Println("ectl sv/service enable <service> | Enable a service at startup")
	fmt.Println("ectl sv/service disable <service> | Disable a service at startup")
	fmt.Println("ectl sv/service status <service> | Show service status")
	fmt.Println("ectl sv/service list | Show all enabled services")
}

func dialSocket() {
	if _, err := os.Stat(path.Join(runstatedir, "esvm/esvm.sock")); err != nil {
		log.Fatalf("Could not find esvm.sock! Error: %s\n", err)
	}

	var err error
	socket, err = net.Dial("unix", path.Join(runstatedir, "esvm/esvm.sock"))
	if err != nil {
		log.Fatalf("Failed to connect to esvm.sock! Error: %s\n", err)
	}

	if err := socket.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Fatalf("Failed to set write deadline! Error: %s\n", err)
	}
}
