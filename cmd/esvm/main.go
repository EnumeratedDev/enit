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
	ServiceRunPath string
	stopChannel    chan bool
}

// Build-time variables
var version = "dev"

var runtimeServiceDir string
var serviceConfigDir string

var Services = make([]EnitService, 0)

var socket net.Listener

func main() {
	// Parse flags
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *printVersion || flag.NArg() != 2 {
		fmt.Printf("Enit Service Manager version %s\n", version)
		os.Exit(0)
	}

	// Set directory variables
	runtimeServiceDir = flag.Arg(0)
	serviceConfigDir = flag.Arg(1)

	if os.Getppid() != 1 {
		fmt.Println("Enit must be run by PID 1!")
		os.Exit(1)
	}

	err := Init()
	if err != nil {
		log.Fatalf("Could not initialize esvm! Error: %s", err)
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

func Init() error {
	if _, err := os.Stat(runtimeServiceDir); err == nil {
		return fmt.Errorf("runtime service directory %s already exists", runtimeServiceDir)
	}

	err := os.MkdirAll(runtimeServiceDir, 0755)
	if err != nil {
		return err
	}

	socket, err = net.Listen("unix", path.Join(runtimeServiceDir, "esvm.sock"))
	if err != nil {
		return err
	}

	if stat, err := os.Stat(serviceConfigDir); err != nil || !stat.IsDir() {
		return nil
	}

	dirEntries, err := os.ReadDir(path.Join(serviceConfigDir, "services"))
	if err != nil {
		return err
	}

	for _, entry := range dirEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esv") {
			bytes, err := os.ReadFile(path.Join(serviceConfigDir, "services", entry.Name()))
			if err != nil {
				log.Printf("Could not read service file at %s!", path.Join(serviceConfigDir, "services", entry.Name()))
			}

			service := EnitService{
				StopCmd:     "",
				stopChannel: make(chan bool),
			}
			if err := yaml.Unmarshal(bytes, &service); err != nil {
				log.Printf("Could not read service file at %s!", path.Join(serviceConfigDir, "services", entry.Name()))
			}

			switch service.Type {
			case "simple", "background":
			default:
				return fmt.Errorf("unknown service type: %s", service.Type)
			}

			switch service.ExitMethod {
			case "stop_command", "kill":
			default:
				return fmt.Errorf("unknown exit method: %s", service.ExitMethod)
			}

			service.ServiceRunPath = path.Join(runtimeServiceDir, service.Name)
			err = os.MkdirAll(path.Join(service.ServiceRunPath), 0755)
			if err != nil {
				return err
			}

			err = service.setCurrentState(EnitServiceUnloaded)
			if err != nil {
				return err
			}

			Services = append(Services, service)

			if err := service.StartService(); err != nil {
				log.Printf("Could not start service %s: %s\n", service.Name, err)
			}
		}
	}

	return nil
}

func Destroy() {
	for _, service := range Services {
		if err := service.StopService(); err != nil {
			log.Printf("Error stopping service %s: %s\n", service.Name, err)
		}
	}
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

	cmd := exec.Command("/bin/sh", "-c", service.StartCmd)
	if err := cmd.Start(); err != nil {
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
			_ = service.setCurrentState(EnitServiceCrashed)
		}

	}()

	err := service.setProcessID(cmd.Process.Pid)
	if err != nil {
		return err
	}

	err = service.setCurrentState(EnitServiceRunning)
	if err != nil {
		return err
	}

	return nil
}

func (service *EnitService) StopService() error {
	if service.GetCurrentState() != EnitServiceRunning {
		return nil
	}

	if service.ExitMethod == "kill" {
		if service.GetProcess() == nil {
			return nil
		}
		go func() { service.stopChannel <- true }()
		err := service.GetProcess().Kill()
		if err != nil {
			return err
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

func checkForServiceCommand() {
	for _, service := range Services {
		if _, err := os.Stat(path.Join(service.ServiceRunPath, "start")); err == nil {
			err := service.StartService()
			if err != nil {
				return
			}
			err = os.Remove(path.Join(service.ServiceRunPath, "start"))
			if err != nil {
				return
			}
		} else if _, err := os.Stat(path.Join(service.ServiceRunPath, "stop")); err == nil {
			err := service.StopService()
			if err != nil {
				return
			}
			err = os.Remove(path.Join(service.ServiceRunPath, "stop"))
			if err != nil {
				return
			}
		} else if _, err := os.Stat(path.Join(service.ServiceRunPath, "restart")); err == nil {
			err := service.RestartService()
			if err != nil {
				return
			}
			err = os.Remove(path.Join(service.ServiceRunPath, "restart"))
			if err != nil {
				return
			}
		}
	}
}

func listenToSocket() {
	conn, err := socket.Accept()
	if err != nil {
		log.Println("Could not accept socket connection!")
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
			log.Fatal(err)
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
