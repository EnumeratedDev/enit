package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	flag "github.com/spf13/pflag"
)

var currentFlagSet *flag.FlagSet
var conn net.Conn

func handleServiceSubcommand() {
	if len(os.Args) == 2 {
		printSvUsage()
		return
	}

	subcommand := os.Args[2]

	switch subcommand {
	case "start", "stop", "restart":
		// Setup flags and help
		currentFlagSet = flag.NewFlagSet(subcommand, flag.ExitOnError)
		currentFlagSet.BoolP("json", "j", false, "Return output in json format")
		setupFlagsAndHelp(currentFlagSet, fmt.Sprintf("ectl %s %s <options> <service>", os.Args[1], subcommand), fmt.Sprintf("%s the specified service", strings.Title(subcommand)), os.Args[3:])

		// Dial esvm socket
		err := dialSocket()
		if err == nil {
			defer conn.Close()
		} else {
			log.Fatalf("Error: %s", err)
		}

		startStopRestartService(subcommand)
	case "enable", "disable":
		// Setup flags and help
		currentFlagSet = flag.NewFlagSet(subcommand, flag.ExitOnError)
		currentFlagSet.BoolP("json", "j", false, "Return output in json format")
		setupFlagsAndHelp(currentFlagSet, fmt.Sprintf("ectl %s %s <options> <service>", os.Args[1], subcommand), fmt.Sprintf("%s the specified service", strings.Title(subcommand)), os.Args[3:])

		enableDisableService(subcommand)
	case "status":
		// Setup flags and help
		currentFlagSet = flag.NewFlagSet("status", flag.ExitOnError)
		currentFlagSet.BoolP("json", "j", false, "Return output in json format")
		setupFlagsAndHelp(currentFlagSet, fmt.Sprintf("ectl %s status <options> <service>", os.Args[1]), "Show service status", os.Args[3:])

		// Dial esvm socket
		err := dialSocket()
		if err == nil {
			defer conn.Close()
		} else {
			log.Fatalf("Error: %s", err)
		}

		showServiceStatus()
	case "list":
		// Setup flags and help
		currentFlagSet = flag.NewFlagSet("list", flag.ExitOnError)
		currentFlagSet.BoolP("json", "j", false, "Return output in json format")
		setupFlagsAndHelp(currentFlagSet, fmt.Sprintf("ectl %s reload <options>", os.Args[1]), "List all services", os.Args[3:])

		// Dial esvm socket
		err := dialSocket()
		if err == nil {
			defer conn.Close()
		} else {
			log.Fatalf("Error: %s", err)
		}

		listAllServices()
	case "reload":
		// Setup flags and help
		currentFlagSet = flag.NewFlagSet("reload", flag.ExitOnError)
		currentFlagSet.BoolP("json", "j", false, "Return output in json format")
		setupFlagsAndHelp(currentFlagSet, fmt.Sprintf("ectl %s reload <options>", os.Args[1]), "Reload all services", os.Args[3:])

		// Dial esvm socket
		err := dialSocket()
		if err == nil {
			defer conn.Close()
		} else {
			log.Fatalf("Error: %s", err)
		}

		reloadAllServices()
	default:
		printSvUsage()
		os.Exit(1)
	}
}

func startStopRestartService(subcommand string) {
	// Get flags
	printJson, _ := currentFlagSet.GetBool("json")

	// Ensure service name argument has been set
	if currentFlagSet.NArg() == 0 {
		fmt.Printf("Usage: ectl service %s <service>\n", subcommand)
		return
	}

	type ServiceCommandJsonStruct struct {
		Command string `json:"command"`
		Service string `json:"service"`
	}
	serviceCommandJson := ServiceCommandJsonStruct{
		Command: subcommand,
		Service: currentFlagSet.Arg(0),
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

	// Read data from the connection.
	data, err := readAllConn(conn)
	if err != nil {
		log.Fatalf("Could not read data from socket! Error: %s\n", err)
		return
	}

	// Print json data if flag is set
	if printJson {
		fmt.Println(string(data))
		return
	}

	// Decoode JSON data
	var returnedJsonData map[string]any
	err = json.Unmarshal(data, &returnedJsonData)
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
}

func enableDisableService(subcommand string) {
	// Get flags
	printJson, _ := currentFlagSet.GetBool("json")

	// Ensure service name argument has been set
	if currentFlagSet.NArg() == 0 {
		fmt.Printf("Usage: ectl service %s <service> [stage]\n", subcommand)
		return
	}

	service := currentFlagSet.Arg(0)

	// Get service stage
	stage := 3
	if subcommand == "disable" {
		stage = 0
	} else if len(currentFlagSet.Args()) > 1 {
		flagStr := currentFlagSet.Arg(1)
		_stage, err := strconv.ParseInt(flagStr, 10, 32)
		if err != nil {
			log.Fatalf("Error: could not parse stage number: %s", err)
		}
		stage = int(_stage)
	}

	verb := "enabled"
	if stage == 0 {
		verb = "disabled"
	}

	// Ensure service exists
	if stage != 0 {
		if !serviceExists(service) {
			if printJson {
				fmt.Printf("{\"error\":\"Service (%s) does not exist\"}\n", verb)
			} else {
				fmt.Printf("Service (%s) does not exist\n", verb)
			}
			os.Exit(1)
		}
	}

	// Return if service is already enabled
	if _, enabledStage := isServiceEnabled(service); enabledStage == stage {
		if printJson {
			fmt.Printf("{\"success\":\"Service (%s) is already %s\"}\n", service, verb)
		} else {
			fmt.Printf("Service (%s) is already %s\n", service, verb)
		}
		return
	}

	// Enable service
	err := setServiceEnabled(service, stage)
	if err != nil {
		verb := "enable"
		if stage == 0 {
			verb = "disable"
		}

		if printJson {
			fmt.Printf("{\"error\":\"Could not %s service! Error: %s\"}\n", verb, err)
		} else {
			fmt.Printf("Could not %s service! Error: %s\n", verb, err)
		}
		os.Exit(1)
	}

	if printJson {
		fmt.Printf("{\"success\":\"Service (%s) was %s sucessfully\"}\n", service, verb)
		return
	} else {
		fmt.Printf("Service (%s) was %s sucessfully\n", service, verb)
	}
}

func showServiceStatus() {
	// Get flags
	printJson, _ := currentFlagSet.GetBool("json")

	// Ensure service name argument has been set
	if len(currentFlagSet.Args()) == 0 {
		fmt.Println("Usage: ectl service status <service>")
		return
	}

	type ServiceCommandJsonStruct struct {
		Command string `json:"command"`
		Service string `json:"service"`
	}
	serviceCommandJson := ServiceCommandJsonStruct{
		Command: "status",
		Service: currentFlagSet.Arg(0),
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

	// Read data from the connection.
	data, err := readAllConn(conn)
	if err != nil {
		log.Fatalf("Could not read data from socket! Error: %s\n", err)
		return
	}

	// Decoode JSON data
	var returnedJsonData map[string]any
	err = json.Unmarshal(data, &returnedJsonData)
	if err != nil {
		log.Fatalf("Could not decode JSON data from connection!")
	}

	if err, ok := returnedJsonData["error"]; ok {
		if printJson {
			fmt.Println(string(data))
			os.Exit(1)
		} else {
			log.Fatal(err)
		}
	}

	// Set is_enabled and stage fields in json data
	returnedJsonData["is_enabled"], returnedJsonData["stage"] = isServiceEnabled(currentFlagSet.Arg(0))

	// Print json data if flag is set
	if printJson {
		data, _ = json.Marshal(returnedJsonData)
		fmt.Println(string(data))
		return
	}

	serviceState := returnedJsonData["state"].(string)
	serviceDescription := returnedJsonData["description"].(string)
	serviceEnabled := returnedJsonData["is_enabled"].(bool)
	serviceStage := returnedJsonData["stage"].(int)
	processID := int(returnedJsonData["process_id"].(float64))

	fmt.Printf("Name: %s\n", currentFlagSet.Arg(0))
	fmt.Printf("Description: %s\n", serviceDescription)
	fmt.Printf("State: %s\n", serviceState)
	if serviceEnabled {
		fmt.Printf("Enabled: %t (Stage %d)\n", serviceEnabled, serviceStage)
	} else {
		fmt.Printf("Enabled: %t\n", serviceEnabled)
	}
	if serviceState == "running" && processID > 0 {
		fmt.Printf("Process ID: %d\n", processID)
	}
}

func listAllServices() {
	// Get flags
	printJson, _ := currentFlagSet.GetBool("json")

	type ServiceCommandJsonStruct struct {
		Command string `json:"command"`
	}
	serviceCommandJson := ServiceCommandJsonStruct{
		Command: "list",
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

	// Read data from the connection.
	data, err := readAllConn(conn)
	if err != nil {
		log.Fatalf("Could not read data from socket! Error: %s\n", err)
		return
	}

	// Decoode JSON data
	var returnedJsonData map[string]any
	err = json.Unmarshal(data, &returnedJsonData)
	if err != nil {
		log.Fatalf("Could not decode JSON data from connection!")
	}

	if err, ok := returnedJsonData["error"]; ok {
		log.Fatal(err)
	}

	// Set is_enabled and stage fields in json data
	for _, serviceMap := range returnedJsonData["services"].([]any) {
		serviceMap.(map[string]any)["is_enabled"], serviceMap.(map[string]any)["stage"] = isServiceEnabled(serviceMap.(map[string]any)["name"].(string))
	}

	// Print json data if flag is set
	if printJson {
		data, _ = json.Marshal(returnedJsonData)
		fmt.Println(string(data))
		return
	}

	for _, serviceMap := range returnedJsonData["services"].([]any) {
		serviceName := serviceMap.(map[string]any)["name"].(string)
		serviceDescription := serviceMap.(map[string]any)["description"].(string)
		serviceState := serviceMap.(map[string]any)["state"].(string)
		serviceEnabled := serviceMap.(map[string]any)["is_enabled"].(bool)
		serviceStage := int(serviceMap.(map[string]any)["stage"].(int))
		processID := int(serviceMap.(map[string]any)["process_id"].(float64))

		fmt.Printf("Name: %s\n", serviceName)
		fmt.Printf("Description: %s\n", serviceDescription)
		fmt.Printf("State: %s\n", serviceState)
		if serviceEnabled {
			fmt.Printf("Enabled: %t (Stage %d)\n", serviceEnabled, serviceStage)
		} else {
			fmt.Printf("Enabled: %t\n", serviceEnabled)
		}
		if serviceState == "running" && processID > 0 {
			fmt.Printf("Process ID: %d\n", processID)
		}
		fmt.Println()
	}
}

func reloadAllServices() {
	// Get flags
	printJson, _ := currentFlagSet.GetBool("json")

	type ServiceCommandJsonStruct struct {
		Command string `json:"command"`
		Service string `json:"service"`
	}
	serviceCommandJson := ServiceCommandJsonStruct{
		Command: "reload",
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

	// Read data from the connection.
	data, err := readAllConn(conn)
	if err != nil {
		log.Fatalf("Could not read data from socket! Error: %s\n", err)
		return
	}

	// Print json data if flag is set
	if printJson {
		fmt.Println(string(data))
		return
	}

	// Decoode JSON data
	var returnedJsonData map[string]any
	err = json.Unmarshal(data, &returnedJsonData)
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
}

func printSvUsage() {
	fmt.Printf("Usage: ectl %s <subcommand> [options] [service]\n", os.Args[1])
	fmt.Println("Description: Manage system services")
	fmt.Println("Sucommands:")
	fmt.Println("  start     Start service")
	fmt.Println("  stop      Stop service")
	fmt.Println("  restart   Restart service")
	fmt.Println("  enable    Enable service")
	fmt.Println("  disable   Disable service")
	fmt.Println("  status    Show service status")
	fmt.Println("  list      List services")
	fmt.Println("  reload    Reload services")
}

func setupFlagsAndHelp(flagset *flag.FlagSet, usage, desc string, args []string) {
	flagset.Usage = func() {
		fmt.Println("Usage: " + usage)
		fmt.Println("Description: " + desc)
		fmt.Println("Options:")
		if !flagset.HasFlags() {
			fmt.Println("  No flags defined")
		}
		flagset.PrintDefaults()
	}
	flagset.Parse(args)
}

func dialSocket() error {
	if _, err := os.Stat(path.Join(runstatedir, "esvm/esvm.sock")); err != nil {
		return fmt.Errorf("could not find socket! Error: %s", err)
	}

	var err error
	conn, err = net.Dial("unix", path.Join(runstatedir, "esvm/esvm.sock"))
	if err != nil {
		return fmt.Errorf("could not connect to socket! Error: %s", err)
	}

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return fmt.Errorf("failed to set socket deadline! Error: %s", err)
	}

	return nil
}

func readAllConn(conn net.Conn) ([]byte, error) {
	var buf bytes.Buffer

	for {
		dataChunk := make([]byte, 1024)

		n, err := conn.Read(dataChunk)
		if err != nil && err != io.EOF {
			return nil, err
		}

		buf.Write(dataChunk[:n])

		if n < 1024 {
			break
		}
	}

	return buf.Bytes(), nil
}
