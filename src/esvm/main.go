package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Build-time variables
var version = "dev"

var runtimeServiceDir string
var serviceConfigDir string

var Services = make([]EnitService, 0)

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
		log.Printf("Could not setup main ESVM logger! Error: %s\n", err)
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

	// Initialize logger and print a header line
	logger = log.New(loggerFile, "[ESVM] ", log.Lshortfile|log.LstdFlags)
	_, err = loggerFile.WriteString("------ " + time.Now().Format(time.UnixDate) + " ------\n")

	return nil
}

func Init() {
	logger.Println("Initializing ESVM...")

	if _, err := os.Stat(runtimeServiceDir); err == nil {
		logger.Fatalf("Could not initialize ESVM! Error: %s", fmt.Errorf("runtime service directory %s already exists", runtimeServiceDir))
	}

	err := os.MkdirAll(runtimeServiceDir, 0755)
	if err != nil {
		logger.Fatalf("Could not initialize ESVM! Error: %s", err)
	}

	socket, err = initSocket()
	if err != nil {
		logger.Fatalf("Could not initialize ESVM! Error: %s", err)
	}

	if stat, err := os.Stat(serviceConfigDir); err != nil || !stat.IsDir() {
		logger.Println("ESVM initialized successfully!")
		return
	}

	dirEntries, err := os.ReadDir(path.Join(serviceConfigDir, "services"))
	if err != nil {
		logger.Fatalf("Could not initialize ESVM! Error: %s", err)
	}

	// Read and initialize service files
	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esv") {
			logger.Printf("Initializing service (%s)...\n", entry.Name())
			bytes, err := os.ReadFile(path.Join(serviceConfigDir, "services", entry.Name()))
			if err != nil {
				logger.Printf("Could not read service file at %s!\n", path.Join(serviceConfigDir, "services", entry.Name()))
				continue
			}

			service := EnitService{
				Name:            "",
				Description:     "",
				Dependencies:    make([]string, 0),
				Type:            "",
				StartCmd:        "",
				ExitMethod:      "",
				StopCmd:         "",
				Restart:         "",
				CrashOnSafeExit: true,
				ServiceRunPath:  "",
				restartCount:    0,
				stopChannel:     make(chan bool),
				LogOutput:       true,
			}
			if err := yaml.Unmarshal(bytes, &service); err != nil {
				logger.Printf("Could not read service file at %s!\n", path.Join(serviceConfigDir, "services", entry.Name()))
				continue
			}

			for _, sv := range Services {
				if sv.Name == service.Name {
					logger.Printf("Service with name (%s) has already been initialized!", service.Name)
				}
			}

			switch service.Type {
			case "simple", "background":
			default:
				logger.Printf("Unknown service type: %s\n", service.Type)
				continue
			}

			switch service.ExitMethod {
			case "stop_command", "kill":
			default:
				logger.Printf("Unknown exit method: %s\n", service.ExitMethod)
				continue
			}

			switch service.Restart {
			case "true", "always":
			default:
				service.Restart = "false"
			}

			service.ServiceRunPath = path.Join(runtimeServiceDir, service.Name)
			err = os.MkdirAll(path.Join(service.ServiceRunPath), 0755)
			if err != nil {
				logger.Fatalf("Could not initialize ESVM! Error: %s", err)
			}

			err = service.setCurrentState(EnitServiceUnloaded)
			if err != nil {
				logger.Fatalf("Could not initialize ESVM! Error: %s", err)
			}

			Services = append(Services, service)

			logger.Printf("Service (%s) has been initialized!\n", service.Name)
		}
	}

	// Get enabled services that meet their dependencies
	servicesWithMetDepends := make([]EnitService, 0)
	for _, service := range Services {
		if service.isEnabled() && len(service.GetUnmetDependencies()) == 0 {
			servicesWithMetDepends = append(servicesWithMetDepends, service)
		}
	}

	// Loop until all enabled services have started or timed out
	for start := time.Now(); time.Since(start) < 60*time.Second; {
		if len(servicesWithMetDepends) == 0 {
			break
		}

		for i := len(servicesWithMetDepends) - 1; i >= 0; i-- {
			service := servicesWithMetDepends[i]
			canStart := true
			for _, dependency := range service.Dependencies {
				if strings.HasPrefix(dependency, "/") {
					// File dependency
					if _, err := os.Stat(dependency); err != nil {
						canStart = false
						break
					}
				} else {
					// Service dependency
					if GetServiceByName(dependency).GetCurrentState() != EnitServiceRunning && GetServiceByName(dependency).GetCurrentState() != EnitServiceCompleted {
						canStart = false
						break
					}
				}
			}
			if canStart {
				err := service.StartService()
				if err != nil {
					logger.Printf("Could not start service (%s)! Error: %s", service.Name, err)
				}
				servicesWithMetDepends = append(servicesWithMetDepends[:i], servicesWithMetDepends[i+1:]...)
			}
		}
	}

	if len(servicesWithMetDepends) > 0 {
		for _, service := range servicesWithMetDepends {
			logger.Printf("Could not start service (%s)! Error: dependencies not met", service.Name)
		}
	}

	logger.Println("ESVM initialized successfully!")
}

func Destroy() {
	logger.Println("Stopping all ESVM services...")
	for _, service := range Services {
		if err := service.StopService(); err != nil {
			logger.Printf("Error stopping service %s! Error: %s\n", service.Name, err)
		}
	}
	logger.Println("All ESVM services have stopped!")
}

func GetServiceByName(name string) *EnitService {
	for _, service := range Services {
		if service.Name == name {
			return &service
		}
	}
	return nil
}
