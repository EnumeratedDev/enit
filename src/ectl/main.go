package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"syscall"
	"time"
)

// Build-time variables
var version = "dev"
var sysconfdir = "/etc/"
var runstatedir = "/var/run/"

var conn net.Conn

func main() {

	// Set and parse flags
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	// Dial esvm socket
	dialSocket()
	defer conn.Close()

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
		} else if flag.Arg(1) == "start" || flag.Arg(1) == "stop" || flag.Arg(1) == "restart" || flag.Arg(1) == "enable" || flag.Arg(1) == "disable" {
			// Ensure service name argument has been set
			if len(flag.Args()) <= 2 {
				fmt.Printf("Usage: ectl service %s <service>\n", flag.Args()[1])
				return
			}

			type ServiceCommandJsonStruct struct {
				Command string `json:"command"`
				Service string `json:"service"`
			}
			serviceCommandJson := ServiceCommandJsonStruct{
				Command: flag.Arg(1),
				Service: flag.Arg(2),
			}

			// Encode struct to json string
			jsonData, err := json.Marshal(serviceCommandJson)
			if err != nil {
				log.Fatalf("Could not encode JSON data! Error: %s\n", err)
			}

			_, err = conn.Write(jsonData)
			if err != nil {
				log.Fatalf("Could not write JSON data to socket! Error: %s\n", err)
			}

			// Create a buffer for incoming data.
			buf := make([]byte, 4096)

			// Read data from the connection.
			n, err := conn.Read(buf)
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}

			// Decoode JSON data
			var returnedJsonData map[string]any
			err = json.Unmarshal(buf[:n], &returnedJsonData)
			if err != nil {
				log.Fatalf("Could not decode JSON data from connection!")
			}

			if err, ok := returnedJsonData["error"]; ok {
				log.Fatal(err)
			} else if msg, ok := returnedJsonData["success"]; ok {
				fmt.Println(msg)
			} else {
				log.Fatal("Connection returned empty string!")
			}

			return
		} else if flag.Args()[1] == "status" {
			// Ensure service name argument has been set
			if len(flag.Args()) <= 2 {
				fmt.Printf("Usage: ectl service %s <service>\n", flag.Args()[1])
				return
			}

			type ServiceCommandJsonStruct struct {
				Command string `json:"command"`
				Service string `json:"service"`
			}
			serviceCommandJson := ServiceCommandJsonStruct{
				Command: flag.Arg(1),
				Service: flag.Arg(2),
			}

			// Encode struct to json string
			jsonData, err := json.Marshal(serviceCommandJson)
			if err != nil {
				log.Fatalf("Could not encode JSON data! Error: %s\n", err)
			}

			_, err = conn.Write(jsonData)
			if err != nil {
				log.Fatalf("Could not write JSON data to socket! Error: %s\n", err)
			}

			// Create a buffer for incoming data.
			buf := make([]byte, 4096)

			// Read data from the connection.
			n, err := conn.Read(buf)
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}

			// Decoode JSON data
			var returnedJsonData map[string]any
			err = json.Unmarshal(buf[:n], &returnedJsonData)
			if err != nil {
				log.Fatalf("Could not decode JSON data from connection!")
			}

			if err, ok := returnedJsonData["error"]; ok {
				log.Fatal(err)
			}

			serviceState := returnedJsonData["state"].(string)
			serviceEnabled := returnedJsonData["is_enabled"].(bool)

			fmt.Printf("Name: %s\n", flag.Arg(2))
			fmt.Printf("State: %s\n", serviceState)
			fmt.Printf("Enabled: %t\n", serviceEnabled)

			return
		} else if flag.Arg(1) == "list" {
			type ServiceCommandJsonStruct struct {
				Command string `json:"command"`
			}
			serviceCommandJson := ServiceCommandJsonStruct{
				Command: flag.Arg(1),
			}

			// Encode struct to json string
			jsonData, err := json.Marshal(serviceCommandJson)
			if err != nil {
				log.Fatalf("Could not encode JSON data! Error: %s\n", err)
			}

			_, err = conn.Write(jsonData)
			if err != nil {
				log.Fatalf("Could not write JSON data to socket! Error: %s\n", err)
			}

			// Create a buffer for incoming data.
			buf := make([]byte, 4096)

			// Read data from the connection.
			n, err := conn.Read(buf)
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}

			// Decoode JSON data
			var returnedJsonData map[string]any
			err = json.Unmarshal(buf[:n], &returnedJsonData)
			if err != nil {
				log.Fatalf("Could not decode JSON data from connection!")
			}

			if err, ok := returnedJsonData["error"]; ok {
				log.Fatal(err)
			}

			for _, serviceMap := range returnedJsonData["services"].([]any) {
				serviceName := serviceMap.(map[string]any)["name"].(string)
				serviceState := serviceMap.(map[string]any)["state"].(string)
				serviceEnabled := serviceMap.(map[string]any)["is_enabled"].(bool)

				fmt.Printf("Name: %s\n", serviceName)
				fmt.Printf("State: %s\n", serviceState)
				fmt.Printf("Enabled: %t\n", serviceEnabled)
				fmt.Println()
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
	conn, err = net.Dial("unix", path.Join(runstatedir, "esvm/esvm.sock"))
	if err != nil {
		log.Fatalf("Failed to connect to esvm.sock! Error: %s\n", err)
	}

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Fatalf("Failed to set write deadline! Error: %s\n", err)
	}
}
