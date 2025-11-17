package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Build-time variables
var version = "dev"

var runtimeServiceDir string
var serviceConfigDir string

var logger *log.Logger
var socket net.Listener

func main() {
	// Parse flags
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *printVersion || flag.NArg() != 2 {
		fmt.Printf("Enit Service Manager version %s\n", version)
		os.Exit(0)
	}

	if os.Getppid() != 1 {
		fmt.Println("Esvm must be run by PID 1!")
		os.Exit(1)
	}

	// Setup main logger
	err := setupESVMLogger()
	if err != nil {
		log.Printf("Error: could not setup main ESVM logger: %s\n", err)
		logger = log.Default()
	}

	// Set directory variables
	runtimeServiceDir = flag.Arg(0)
	serviceConfigDir = flag.Arg(1)

	Init()
	if err != nil {

	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		Destroy()
		os.Exit(0)
	}()

	for {
		listenToSocket()
	}
}

func setupESVMLogger() error {
	// Create esvm log directory
	err := os.MkdirAll("/var/log/esvm", 0755)
	if err != nil {
		return err
	}

	// Create esvm old log directory
	err = os.MkdirAll("/var/log/esvm/old", 0755)
	if err != nil {
		return err
	}

	// Move old log file
	if _, err := os.Stat("/var/log/esvm/esvm.log"); err == nil {
		os.Rename("/var/log/esvm/esvm.log", "/var/log/esvm/old/esvm.log")
	}

	// Open new log file
	loggerFile, err := os.OpenFile("/var/log/esvm/esvm.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	// Setup multiwriter
	w := io.MultiWriter(loggerFile, os.Stderr)

	// Initialize logger and print a header line
	logger = log.New(w, "[ESVM] ", log.Lshortfile|log.LstdFlags)
	_, err = loggerFile.WriteString("------ " + time.Now().Format(time.UnixDate) + " ------\n")

	return nil
}

func Init() {
	logger.Println("Initializing ESVM...")

	if _, err := os.Stat(runtimeServiceDir); err == nil {
		logger.Fatalf("Error: could not initialize ESVM: %s", fmt.Errorf("runtime service directory %s already exists", runtimeServiceDir))
	}

	err := os.MkdirAll(runtimeServiceDir, 0755)
	if err != nil {
		logger.Fatalf("Error: could not initialize ESVM: %s", err)
	}

	socket, err = initSocket()
	if err != nil {
		logger.Fatalf("Error: could not initialize ESVM: %s", err)
	}

	if stat, err := os.Stat(serviceConfigDir); err != nil || !stat.IsDir() {
		logger.Println("ESVM initialized successfully!")
		return
	}

	dirEntries, err := os.ReadDir(path.Join(serviceConfigDir, "services"))
	if err != nil {
		logger.Fatalf("Error: Could not initialize ESVM: %s", err)
	}

	// Read and initialize service files
	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esv") {
			filepath := path.Join(serviceConfigDir, "services", entry.Name())
			LoadService(filepath)
		}
	}

	// Read enabled services
	EnabledServices := ReadEnabledServices()

	// Start enabled services
	stages := slices.Collect(maps.Keys(EnabledServices))
	slices.Sort(stages)
	for stage := 1; stage <= stages[len(stages)-1]; stage++ {
		logger.Printf("Starting stage %d services...", stage)

		services := EnabledServices[stage]
		remainingServices := len(services)
		for remainingServices != 0 {
			for _, serviceName := range services {
				service := GetServiceByName(serviceName)
				if service == nil {
					remainingServices--
					continue
				}

				err := service.StartService()
				if err != nil {
					logger.Printf("Error: could not start service (%s): %s", service.Name, err)
				}
				remainingServices--
			}
		}
	}

	logger.Println("ESVM initialized successfully!")
}

func Reload() {
	logger.Println("Reloading all ESVM services...")

	dirEntries, err := os.ReadDir(path.Join(serviceConfigDir, "services"))
	if err != nil {
		logger.Fatalf("Error: Could not initialize ESVM: %s", err)
	}

	// Read and load service files
	servicesToRemove := slices.Clone(Services)
	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esv") {
			filepath := path.Join(serviceConfigDir, "services", entry.Name())
			LoadService(filepath)
			servicesToRemove = slices.DeleteFunc(servicesToRemove, func(sv *EnitService) bool {
				return sv.Filepath == filepath
			})
		}
	}

	// Reload services that had their esv file removed
	for _, service := range servicesToRemove {
		LoadService(service.Filepath)
	}

	logger.Println("All ESVM services have been reloaded!")
}

func Destroy() {
	logger.Println("Stopping all ESVM services...")

	// Loop through all started services in reverse
	for i := len(startedServicesOrder) - 1; i >= 0; i-- {
		// Get service by name
		service := GetServiceByName(startedServicesOrder[i])
		if service == nil {
			continue
		}

		// Stop service
		if err := service.StopService(); err != nil {
			logger.Printf("Error: could not stop service (%s): %s", service.Name, err)
		}
	}

	logger.Println("All ESVM services have stopped!")
}

func GetServiceByName(name string) *EnitService {
	for _, service := range Services {
		if service.Name == name {
			return service
		}
	}
	return nil
}
