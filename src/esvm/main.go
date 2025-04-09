package main

import (
	"flag"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type EnitServiceState uint8

const (
	EnitServiceUnknown EnitServiceState = iota
	EnitServiceUnloaded
	EnitServiceRunning
	EnitServiceStopped
	EnitServiceCrashed
	EnitServiceCompleted
)

type EnitService struct {
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description,omitempty"`
	Dependencies    []string `yaml:"dependencies,omitempty"`
	Type            string   `yaml:"type"`
	StartCmd        string   `yaml:"start_cmd"`
	ExitMethod      string   `yaml:"exit_method"`
	CrashOnSafeExit bool     `yaml:"crash_on_safe_exit"`
	StopCmd         string   `yaml:"stop_cmd,omitempty"`
	Restart         string   `yaml:"restart,omitempty"`
	LogOutput       bool     `yaml:"log_output,omitempty"`
	ServiceRunPath  string
	restartCount    int
	stopChannel     chan bool
}

// Build-time variables
var version = "dev"

var runtimeServiceDir string
var serviceConfigDir string

var Services = make([]EnitService, 0)
var EnabledServices = make([]string, 0)

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
	err := os.MkdirAll("/var/log/esvm", 0755)
	if err != nil {
		return err
	}
	loggerFile, err := os.OpenFile("/var/log/esvm/esvm.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	logger = log.New(loggerFile, "[ESVM] ", log.Lshortfile|log.LstdFlags)
	// Print an empty line as separator
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

	socket, err = net.Listen("unix", path.Join(runtimeServiceDir, "esvm.sock"))
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

	// Get enabled services
	if _, err := os.Stat(path.Join(serviceConfigDir, "enabled_services")); err == nil {
		file, err := os.ReadFile(path.Join(serviceConfigDir, "enabled_services"))
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(file), "\n") {
			if line != "" {
				EnabledServices = append(EnabledServices, line)
			}
		}
	}

	// Get enabled services that meet their dependencies
	servicesWithMetDepends := make([]EnitService, 0)
	for _, service := range Services {
		if slices.Contains(EnabledServices, service.Name) && len(service.GetUnmetDependencies()) == 0 {
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

func (service *EnitService) GetUnmetDependencies() (missingDependencies []string) {
	for _, dependency := range service.Dependencies {
		if strings.HasPrefix(dependency, "/") {
			// File dependency
			if _, err := os.Stat(dependency); err != nil {
				missingDependencies = append(missingDependencies, dependency)
			}
		} else {
			// Service dependency
			depService := GetServiceByName(dependency)
			if depService == nil {
				missingDependencies = append(missingDependencies, dependency)
			}
		}
	}

	return missingDependencies
}

func (service *EnitService) GetProcess() *os.Process {
	bytes, err := os.ReadFile(path.Join(service.ServiceRunPath, "process"))
	if err != nil {
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(bytes)))
	if err != nil {
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	return process
}

func (service *EnitService) setProcessID(pid int) error {
	if err := os.WriteFile(path.Join(service.ServiceRunPath, "process"), []byte(strconv.Itoa(pid)), 0644); err != nil {
		return err
	}
	return nil
}

func (service *EnitService) GetCurrentState() EnitServiceState {
	bytes, err := os.ReadFile(path.Join(service.ServiceRunPath, "state"))
	if err != nil {
		return EnitServiceUnknown
	}

	state, err := strconv.Atoi(strings.TrimSpace(string(bytes)))
	if err != nil {
		return EnitServiceUnknown
	}
	return EnitServiceState(state)
}

func (service *EnitService) setCurrentState(state EnitServiceState) error {
	if err := os.WriteFile(path.Join(service.ServiceRunPath, "state"), []byte(strconv.Itoa(int(state))), 0644); err != nil {
		return err
	}
	return nil
}

func (service *EnitService) GetLogFile() (file *os.File, err error) {
	err = os.MkdirAll(path.Join("/var/log/esvm/"), 0755)
	if err != nil {
		return nil, err
	}

	file, err = os.OpenFile(path.Join("/var/log/esvm/", service.Name+".log"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	_, err = file.WriteString("------ " + time.Now().Format(time.UnixDate) + " ------\n")
	if err != nil {
		file.Close()
		return nil, err
	}

	return file, nil
}

func (service *EnitService) StartService() error {
	if service == nil {
		return nil
	}
	if service.GetCurrentState() == EnitServiceRunning {
		return nil
	}

	logger.Printf("Starting service (%s)...\n", service.Name)

	// Get log file if service logs output
	var logFile *os.File
	if service.LogOutput {
		var err error
		logFile, err = service.GetLogFile()
		if err != nil {
			return err
		}
	}

	cmd := exec.Command("/bin/sh", "-c", "exec "+service.StartCmd)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		// Close log file if not nil
		if logFile != nil {
			logFile.Close()
		}

		return err
	}

	err := service.setProcessID(cmd.Process.Pid)
	if err != nil {
		// Close log file if not nil
		if logFile != nil {
			logFile.Close()
		}

		return err
	}

	err = service.setCurrentState(EnitServiceRunning)
	if err != nil {
		// Close log file if not nil
		if logFile != nil {
			logFile.Close()
		}

		return err
	}

	go func() {
		err := cmd.Wait()

		// Close log file if not nil
		if logFile != nil {
			logFile.Close()
		}

		select {
		case <-service.stopChannel:
			service.restartCount = 0
			_ = service.setCurrentState(EnitServiceStopped)
		default:
			if service.Type == "simple" && err == nil {
				service.restartCount = 0
				_ = service.setCurrentState(EnitServiceCompleted)
				return
			}
			if !service.CrashOnSafeExit {
				logger.Printf("Service (%s) has exited\n", service.Name)
				_ = service.setCurrentState(EnitServiceStopped)
			} else {
				logger.Printf("Service (%s) has crashed!\n", service.Name)
				_ = service.setCurrentState(EnitServiceCrashed)
			}

			if service.Restart == "always" {
				_ = service.StartService()
			} else if service.Restart == "true" && service.restartCount < 5 {
				service.restartCount++
				_ = service.StartService()
			}
		}
	}()

	logger.Printf("Service (%s) has started!\n", service.Name)

	return nil
}

func (service *EnitService) StopService() error {
	if service.GetCurrentState() != EnitServiceRunning {
		return nil
	}

	logger.Printf("Stopping service (%s)...\n", service.Name)

	if service.ExitMethod == "kill" {
		process := service.GetProcess()
		if err := process.Signal(syscall.Signal(0)); err != nil {
			logger.Printf("Service (%s) has stopped. (Process already dead)\n", service.Name)
			return nil
		}

		go func() { service.stopChannel <- true }()

		err := service.GetProcess().Signal(syscall.SIGTERM)
		if err != nil {
			return err
		}

		exit := false
		for timeout := time.After(5 * time.Second); ; {
			if exit {
				break
			}
			select {
			case <-timeout:
				logger.Println("Process took too long to finish. Forcefully killing process...")
				err := service.GetProcess().Kill()
				if err != nil {
					return err
				}
				exit = true
			default:
				if process == nil {
					exit = true
					break
				}
				err = process.Signal(syscall.Signal(0))
				if err != nil {
					exit = true
				}
			}
		}
	} else {
		cmd := exec.Command("/bin/sh", "-c", service.StopCmd)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	err := service.setCurrentState(EnitServiceStopped)
	if err != nil {
		return err
	}

	err = service.setProcessID(0)
	if err != nil {
		return err
	}

	logger.Printf("Service (%s) has stopped!\n", service.Name)

	return nil
}

func (service *EnitService) RestartService() error {
	if err := service.StopService(); err != nil {
		return err
	}

	if err := service.StartService(); err != nil {
		return err
	}

	return nil
}

func listenToSocket() {
	conn, err := socket.Accept()
	if err != nil {
		logger.Println("Could not accept socket connection!")
		panic(err)
	}

	// Handle the connection in a separate goroutine.
	go func(conn net.Conn) {
		defer conn.Close()
		// Create a buffer for incoming data.
		buf := make([]byte, 4096)

		// Read data from the connection.
		n, err := conn.Read(buf)
		if err == io.EOF {
			return
		}
		if err != nil {
			logger.Fatal(err)
		}

		command := string(buf[:n])
		commandSplit := strings.Split(command, " ")

		if len(commandSplit) >= 2 {
			if commandSplit[0] == "start" {
				service := GetServiceByName(commandSplit[1])
				if service == nil {
					_, err := conn.Write([]byte("service not found"))
					if err != nil {
						return
					}
				}
				if err := service.StartService(); err != nil {
					_, err := conn.Write([]byte("could not start service"))
					if err != nil {
						return
					}
				}
				_, err := conn.Write([]byte("ok"))
				if err != nil {
					return
				}
			} else if commandSplit[0] == "stop" {
				service := GetServiceByName(commandSplit[1])
				if service == nil {
					_, err := conn.Write([]byte("service not found"))
					if err != nil {
						return
					}
				}
				if err := service.StopService(); err != nil {
					_, err := conn.Write([]byte("could not stop service"))
					if err != nil {
						return
					}
				}
				_, err := conn.Write([]byte("ok"))
				if err != nil {
					return
				}
			} else if commandSplit[0] == "restart" {
				service := GetServiceByName(commandSplit[1])
				if service == nil {
					_, err := conn.Write([]byte("service not found"))
					if err != nil {
						return
					}
				}
				if err := service.RestartService(); err != nil {
					_, err := conn.Write([]byte("could not restart service"))
					if err != nil {
						return
					}
				}
				_, err := conn.Write([]byte("ok"))
				if err != nil {
					return
				}
			}
		}
	}(conn)
}
