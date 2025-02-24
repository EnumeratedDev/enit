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
	Name           string `yaml:"name"`
	Description    string `yaml:"description,omitempty"`
	Type           string `yaml:"type"`
	StartCmd       string `yaml:"start_cmd"`
	ExitMethod     string `yaml:"exit_method"`
	StopCmd        string `yaml:"stop_cmd,omitempty"`
	Restart        bool   `yaml:"restart,omitempty"`
	ServiceRunPath string
	stopChannel    chan bool
}

// Build-time variables
var version = "dev"

var runtimeServiceDir string
var serviceConfigDir string

var Services = make([]EnitService, 0)

var logger *log.Logger
var socket net.Listener

func main() {
	loggerFile, err := os.OpenFile("/var/log/esvm.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Error opening /var/log/esvm/esvm.log: %v", err)
	}
	logger = log.New(loggerFile, "[ESVM] ", log.Lshortfile|log.LstdFlags)
	// Print an empty line as separator
	logger.Println()

	// Parse flags
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *printVersion || flag.NArg() != 2 {
		fmt.Printf("Enit Service Manager version %s\n", version)
		os.Exit(0)
	}

	if os.Getppid() != 1 {
		fmt.Println("Enit must be run by PID 1!")
		os.Exit(1)
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
		loggerFile.Close()
		os.Exit(0)
	}()

	for {
		listenToSocket()
	}
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

	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esv") {
			logger.Printf("Initializing service (%s)...\n", entry.Name())
			bytes, err := os.ReadFile(path.Join(serviceConfigDir, "services", entry.Name()))
			if err != nil {
				logger.Printf("Could not read service file at %s!\n", path.Join(serviceConfigDir, "services", entry.Name()))
				continue
			}

			service := EnitService{
				Name:           "",
				Description:    "",
				Type:           "",
				StartCmd:       "",
				ExitMethod:     "",
				StopCmd:        "",
				Restart:        false,
				ServiceRunPath: "",
				stopChannel:    make(chan bool),
			}
			if err := yaml.Unmarshal(bytes, &service); err != nil {
				logger.Printf("Could not read service file at %s!\n", path.Join(serviceConfigDir, "services", entry.Name()))
				continue
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

			if err := service.StartService(); err != nil {
				logger.Printf("Could not start service %s: %s\n", service.Name, err)
			}

			logger.Printf("Service (%s) has been initialized!\n", service.Name)
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

func (service *EnitService) StartService() error {
	if service == nil {
		return nil
	}
	if service.GetCurrentState() == EnitServiceRunning {
		return nil
	}

	logger.Printf("Starting service (%s)...\n", service.Name)

	cmd := exec.Command("/bin/sh", "-c", "exec "+service.StartCmd)
	if err := cmd.Start(); err != nil {
		return err
	}

	err := service.setProcessID(cmd.Process.Pid)
	if err != nil {
		return err
	}

	err = service.setCurrentState(EnitServiceRunning)
	if err != nil {
		return err
	}

	go func() {
		err := cmd.Wait()
		select {
		case <-service.stopChannel:
			_ = service.setCurrentState(EnitServiceStopped)
		default:
			if service.Type == "simple" && err == nil {
				_ = service.setCurrentState(EnitServiceCompleted)
			}
			logger.Printf("Service (%s) has crashed!\n", service.Name)
			_ = service.setCurrentState(EnitServiceCrashed)

			if service.Restart {
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
