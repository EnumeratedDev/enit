package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
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

var EnitServiceStateNames map[EnitServiceState]string = map[EnitServiceState]string{
	EnitServiceUnknown:   "unknown",
	EnitServiceUnloaded:  "unloaded",
	EnitServiceRunning:   "running",
	EnitServiceStopped:   "stopped",
	EnitServiceCrashed:   "crashed",
	EnitServiceCompleted: "completed",
}

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
	processID       int
	restartCount    int
	stopChannel     chan bool
}

var Services = make([]*EnitService, 0)
var EnabledServices = make(map[int][]string)
var startedServicesOrder = make([]string, 0)

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
	process, _ := os.FindProcess(service.processID)

	return process
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
	// Create esvm log directory
	err = os.MkdirAll("/var/log/esvm", 0755)
	if err != nil {
		return nil, err
	}

	// Create esvm old log directory
	err = os.MkdirAll("/var/log/esvm/old", 0755)
	if err != nil {
		return nil, err
	}

	// Move old log file
	if _, err := os.Stat(path.Join("/var/log/esvm/", service.Name+".log")); err == nil {
		os.Rename(path.Join("/var/log/esvm/", service.Name+".log"), path.Join("/var/log/esvm/old", service.Name+".log"))
	}

	// Open new log file
	file, err = os.OpenFile(path.Join("/var/log/esvm/", service.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	// Initialize logger and print a header line
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

	service.processID = cmd.Process.Pid

	err := service.setCurrentState(EnitServiceRunning)
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
		default:
			if service.Type == "simple" && err == nil {
				service.restartCount = 0
				if service.ExitMethod != "stop_command" {
					_ = service.setCurrentState(EnitServiceCompleted)
				} else {
					_ = service.setCurrentState(EnitServiceRunning)
				}
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

		service.processID = 0
	}()

	// Add to started services order slice
	if !slices.Contains(startedServicesOrder, service.Name) {
		startedServicesOrder = append(startedServicesOrder, service.Name)
	}

	logger.Printf("Service (%s) has started!\n", service.Name)

	return nil
}

func (service *EnitService) StopService() error {
	if service.GetCurrentState() != EnitServiceRunning {
		return nil
	}

	logger.Printf("Stopping service (%s)...", service.Name)

	newServiceStatus := EnitServiceCrashed
	defer func() {
		service.setCurrentState(newServiceStatus)
		service.processID = 0
	}()

	if service.ExitMethod == "kill" {
		if err := service.GetProcess().Signal(syscall.Signal(0)); err != nil {
			newServiceStatus = EnitServiceStopped
			logger.Printf("Service (%s) has stopped (Process already dead)", service.Name)
			return nil
		}

		go func() { service.stopChannel <- true }()

		// Send SIGTERM signal to process
		if err := service.GetProcess().Signal(syscall.SIGTERM); err != nil {
			service.GetProcess().Signal(syscall.SIGKILL)
			return fmt.Errorf("could not stop process gracefully")
		}

		// Check if the process has stopped gracefully, otherwise send sigkill on timeout
		exited := make(chan bool)
		go func() {
			for {
				if err := service.GetProcess().Signal(syscall.Signal(0)); err != nil {
					break
				}
			}
			exited <- true
		}()

		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			service.GetProcess().Signal(syscall.SIGKILL)
			return fmt.Errorf("could not stop process gracefully")
		}
	} else {
		cmd := exec.Command("/bin/sh", "-c", service.StopCmd)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	newServiceStatus = EnitServiceStopped
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

// Functions will be rewritten at some point to allow enabling unloaded services

func (service *EnitService) isEnabled() (bool, int) {
	for stage, services := range EnabledServices {
		if slices.Contains(services, service.Name) {
			return true, stage
		}
	}

	return false, 0
}

func (service *EnitService) SetEnabled(stage int) error {
	// Get current service enabled status
	_, s := service.isEnabled()

	// Return if service is already in correct state
	if s == stage {
		return nil
	}

	// Remove service from current stage
	EnabledServices[s] = slices.DeleteFunc(EnabledServices[s], func(name string) bool {
		return name == service.Name
	})
	if len(EnabledServices[s]) == 0 {
		delete(EnabledServices, s)
	}

	// Add service to stage
	if stage != 0 {
		EnabledServices[stage] = append(EnabledServices[stage], service.Name)
	}

	// Save enabled services to file
	data, err := yaml.Marshal(EnabledServices)
	if err != nil {
		return err
	}
	err = os.WriteFile(path.Join(serviceConfigDir, "enabled_services"), data, 0644)
	if err != nil {
		return err
	}

	return nil
}

func ReadEnabledServices() error {
	data, err := os.ReadFile(path.Join(serviceConfigDir, "enabled_services"))
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(data, &EnabledServices)
	if err != nil {
		// Assume old plain text format
		for _, service := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			EnabledServices[3] = append(EnabledServices[3], service)
		}

		// Update enabled_services file
		data, err := yaml.Marshal(EnabledServices)
		if err != nil {
			return err
		}
		err = os.WriteFile(path.Join(serviceConfigDir, "enabled_services"), data, 0644)
		if err != nil {
			return err
		}

		return nil
	}

	return nil
}
