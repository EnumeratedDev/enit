package main

import (
	"flag"
	"fmt"
	"gopkg.in/yaml.v3"
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
			if _, err := os.Stat(path.Join(runstatedir, "esvm")); err != nil {
				log.Fatalf("Could not list services! Error: %s\n", err)
			}

			entries, err := os.ReadDir(path.Join(runstatedir, "esvm"))
			if err != nil {
				log.Fatalf("Could not list services! Error: %s\n", err)
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}

				state := getServiceState(entry.Name())
				enabled := strconv.FormatBool(isServiceEnabled(entry.Name()))
				enabled = strings.ToUpper(enabled[:1]) + strings.ToLower(enabled[1:])

				fmt.Println("Service name: " + entry.Name())
				fmt.Printf("    State: %s\n", state)
				fmt.Printf("    Enabled: %s\n", enabled)
			}
			return
		} else if len(flag.Args()) <= 2 {
			fmt.Printf("Usage: ectl service %s <service>\n", flag.Args()[1])
			return
		} else if flag.Args()[1] == "start" {
			if _, err := os.Stat(path.Join(runstatedir, "esvm", flag.Args()[2])); err != nil {
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
			if _, err := os.Stat(path.Join(runstatedir, "esvm", flag.Args()[2])); err != nil {
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
			if _, err := os.Stat(path.Join(runstatedir, "esvm", flag.Args()[2])); err != nil {
				log.Fatalf("Could not restart service! Error: %s\n", err)
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
		} else if flag.Args()[1] == "enable" {
			// Check if service exists
			found := false
			entries, err := os.ReadDir(path.Join(sysconfdir, "esvm/services/"))
			if err != nil {
				log.Fatalf("Could not enable service! Error: %s\n", err)
			}
			type minimalServiceStruct struct {
				Name string `yaml:"name"`
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".esv") {
					continue
				}

				bytes, err := os.ReadFile(path.Join(sysconfdir, "esvm/services", entry.Name()))
				if err != nil {
					log.Fatalf("Could not enable service! Error: %s\n", err)
				}

				sv := minimalServiceStruct{Name: ""}
				err = yaml.Unmarshal(bytes, &sv)
				if err != nil {
					log.Fatalf("Could not enable service! Error: %s\n", err)
				}

				if sv.Name == flag.Args()[2] {
					found = true
					break
				}
			}
			if !found {
				log.Fatalf("Service does not exist!")
			}

			if _, err := os.Stat(path.Join(sysconfdir, "esvm/enabled_services")); err != nil {
				err := os.WriteFile(path.Join(sysconfdir, "esvm/enabled_services"), []byte(flag.Args()[2]+"\n"), 0644)
				if err != nil {
					log.Fatalf("Could not enable service! Error: %s\n", err)
				}
				return
			}

			file, err := os.ReadFile(path.Join(sysconfdir, "esvm/enabled_services"))
			if err != nil {
				log.Fatalf("Could not enable service! Error: %s\n", err)
			}
			for _, line := range strings.Split(string(file), "\n") {
				if strings.TrimSpace(line) == flag.Args()[2] {
					fmt.Println("Service is already enabled!")
					return
				}
			}

			err = os.WriteFile(path.Join(sysconfdir, "esvm/enabled_services"), []byte(string(file)+flag.Args()[2]+"\n"), 0644)
			if err != nil {
				log.Fatalf("Could not enable service! Error: %s\n", err)
			}

			fmt.Printf("Service (%s) has been enabled!\n", flag.Args()[2])
			return
		} else if flag.Args()[1] == "disable" {
			if _, err := os.Stat(path.Join(sysconfdir, "esvm/enabled_services")); err != nil {
				fmt.Println("Service is already disabled!")
				return
			}

			file, err := os.ReadFile(path.Join(sysconfdir, "esvm/enabled_services"))
			if err != nil {
				log.Fatalf("Could not disable service! Error: %s\n", err)
			}

			lines := strings.Split(string(file), "\n")
			found := false
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if strings.TrimSpace(line) == flag.Args()[2] {
					lines = append(lines[:i], lines[i+1:]...)
					found = true
				} else if strings.TrimSpace(line) == "" {
					lines = append(lines[:i], lines[i+1:]...)
				}
			}

			if !found {
				fmt.Println("Service is already disabled!")
				return
			}

			err = os.WriteFile(path.Join(sysconfdir, "esvm/enabled_services"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
			if err != nil {
				log.Fatalf("Could not disable service! Error: %s\n", err)
			}

			fmt.Printf("Service (%s) has been disabled!\n", flag.Args()[2])
			return
		} else if flag.Args()[1] == "status" {
			if _, err := os.Stat(path.Join(runstatedir, "esvm", flag.Args()[2])); err != nil {
				log.Fatalf("Could not get service status! Error: %s\n", err)
			}

			state := getServiceState(flag.Args()[2])
			enabled := strconv.FormatBool(isServiceEnabled(flag.Args()[2]))
			enabled = strings.ToUpper(enabled[:1]) + strings.ToLower(enabled[1:])

			fmt.Println("Service name: " + flag.Args()[2])
			fmt.Printf("    State: %s\n", state)
			fmt.Printf("    Enabled: %s\n", enabled)
			return
		}
	}

	printUsage()
	os.Exit(1)
}

func getServiceState(serviceName string) string {
	if _, err := os.Stat(path.Join(runstatedir, "esvm", serviceName)); err != nil {
		return ""
	}

	var state uint64
	bytes, err := os.ReadFile(path.Join(runstatedir, "esvm", serviceName, "state"))
	if err != nil {
		state = 0
	}
	state, err = strconv.ParseUint(string(bytes), 10, 8)

	switch state {
	case 1:
		return "Unloaded"
	case 2:
		return "Running"
	case 3:
		return "Stopped"
	case 4:
		return "Crashed"
	case 5:
		return "Completed"
	default:
		return "Unknown"
	}
}

func isServiceEnabled(serviceName string) bool {
	if _, err := os.Stat(path.Join(sysconfdir, "esvm/enabled_services")); err != nil {
		return false
	}

	file, err := os.ReadFile(path.Join(sysconfdir, "esvm/enabled_services"))
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(file), "\n") {
		if strings.TrimSpace(line) == serviceName {
			return true
		}
	}

	return false
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
