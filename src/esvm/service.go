package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type EnitServiceState uint8

const (
	EnitServiceUnknown EnitServiceState = iota
	EnitServiceUnloaded
	EnitServiceStarting
	EnitServiceRunning
	EnitServiceStopped
	EnitServiceCrashed
	EnitServiceCompleted
)

var EnitServiceStateNames map[EnitServiceState]string = map[EnitServiceState]string{
	EnitServiceUnknown:   "unknown",
	EnitServiceUnloaded:  "unloaded",
	EnitServiceStarting:  "starting",
	EnitServiceRunning:   "running",
	EnitServiceStopped:   "stopped",
	EnitServiceCrashed:   "crashed",
	EnitServiceCompleted: "completed",
}

type EnitService struct {
	Name             string `yaml:"name"`
	Description      string `yaml:"description,omitempty"`
	Type             string `yaml:"type"`
	StartCmd         string `yaml:"start_cmd"`
	ExitMethod       string `yaml:"exit_method"`
	CrashOnSafeExit  bool   `yaml:"crash_on_safe_exit"`
	StopCmd          string `yaml:"stop_cmd,omitempty"`
	Restart          string `yaml:"restart,omitempty"`
	ReadyFd          int    `yaml:"ready_fd"`
	Setpgid          bool   `yaml:"setpgid"`
	LogOutput        bool   `yaml:"log_output,omitempty"`
	Filepath         string
	filepathChecksum [32]byte
	state            EnitServiceState
	processID        int
	restartCount     int
	stopChannel      chan bool
	shouldReload     bool
}

var Services = make([]*EnitService, 0)
var startedServicesOrder = make([]string, 0)

func (service *EnitService) GetProcess() *os.Process {
	process, _ := os.FindProcess(service.processID)

	return process
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

func (service *EnitService) ReloadService() {
	bytes, err := os.ReadFile(service.Filepath)
	checksum := sha256.Sum256(bytes)
	if slices.Equal(checksum[:], service.filepathChecksum[:]) {
		return
	}

	if service.state == EnitServiceStarting || service.state == EnitServiceRunning {
		service.shouldReload = true
		logger.Printf("Warning: Service (%s) is currently running and will be reloaded when stopped\n", service.Name)
		return
	}
	service.shouldReload = false

	logger.Printf("Reloading service (%s)...\n", service.Filepath)

	if os.IsNotExist(err) {
		Services = slices.DeleteFunc(Services, func(sv *EnitService) bool {
			return sv == service
		})
		logger.Printf("Service (%s) has been removed\n", service.Name)
		return
	} else if err != nil {
		logger.Printf("Error: Could not read service file (%s)", service.Filepath)
		return
	}

	newService := EnitService{
		Name:             "",
		Description:      "",
		Type:             "",
		StartCmd:         "",
		ExitMethod:       "",
		StopCmd:          "",
		Restart:          "",
		Setpgid:          true,
		CrashOnSafeExit:  true,
		LogOutput:        true,
		Filepath:         service.Filepath,
		filepathChecksum: checksum,
		restartCount:     service.restartCount,
		stopChannel:      service.stopChannel,
		state:            service.state,
	}
	if err := yaml.Unmarshal(bytes, &newService); err != nil {
		logger.Printf("Error: could not read service file %s", service.Filepath)
		return
	}

	for _, sv := range Services {
		if sv.Name == newService.Name && sv != service {
			logger.Printf("Error: service with name (%s) has already been initialized", service.Name)
		}
	}

	switch newService.Type {
	case "simple", "background":
	default:
		logger.Printf("Error: unknown service type (%s)", newService.Type)
		return
	}

	switch newService.ExitMethod {
	case "stop_command", "kill":
	default:
		logger.Printf("Error: unknown exit method (%s)\n", newService.ExitMethod)
		return
	}

	switch newService.Restart {
	case "true", "always":
	default:
		newService.Restart = "false"
	}

	for i, sv := range Services {
		if sv == service {
			Services[i] = &newService
		}
	}

	logger.Printf("Service (%s) has been reloaded!\n", newService.Name)
}

func (service *EnitService) StartService() (err error) {
	if service == nil {
		return nil
	}
	if service.state == EnitServiceRunning {
		return nil
	}

	logger.Printf("Starting service (%s)...\n", service.Name)

	// Get log file if service logs output
	var logFile *os.File
	if service.LogOutput {
		logFile, err = service.GetLogFile()
		if err != nil {
			return err
		}
	}

	cmd := exec.Command("/bin/sh", "-c", "exec "+service.StartCmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: service.Setpgid, Pgid: 0}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	var pipeReader, pipeWriter *os.File
	if service.ReadyFd > 2 {
		pipeReader, pipeWriter, err = os.Pipe()
		if err != nil {
			// Close log file if not nil
			if logFile != nil {
				logFile.Close()
			}

			return err
		}

		err := pipeReader.SetDeadline(time.Now().Add(10 * time.Second))
		if err != nil {
			// Close log file if not nil
			if logFile != nil {
				logFile.Close()
			}

			return err
		}

		for i := 3; i < service.ReadyFd; i++ {
			cmd.ExtraFiles = append(cmd.ExtraFiles, nil)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, pipeWriter)
	}
	if err := cmd.Start(); err != nil {
		// Close log file if not nil
		if logFile != nil {
			logFile.Close()
		}

		return err
	}

	pid := cmd.Process.Pid
	service.processID = cmd.Process.Pid
	service.state = EnitServiceStarting

	// Wait for data from pipe
	if pipeReader != nil {
		buffer := make([]byte, 1)
		_, err := io.ReadAtLeast(pipeReader, buffer, 1)
		if err != nil {
			// Close log file if not nil
			if logFile != nil {
				logFile.Close()
			}

			// Kill process and children
			syscall.Kill(-pid, syscall.SIGKILL)

			service.processID = 0
			service.state = EnitServiceCrashed

			return err
		}
	}

	service.state = EnitServiceRunning

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
			// Kill remaining child processes
			syscall.Kill(-pid, syscall.SIGKILL)

			if service.Type == "simple" && err == nil {
				service.restartCount = 0
				if service.ExitMethod != "stop_command" {
					service.state = EnitServiceCompleted

					// Reload service if needed
					if service.shouldReload {
						service.ReloadService()
						if GetServiceByName(service.Name) == nil {
							return
						}
					}
				} else {
					service.state = EnitServiceRunning
				}
				return
			}
			if !service.CrashOnSafeExit {
				logger.Printf("Service (%s) has exited\n", service.Name)
				service.state = EnitServiceStopped
			} else {
				logger.Printf("Service (%s) has crashed!\n", service.Name)
				service.state = EnitServiceCrashed
			}

			// Reload service if needed
			if service.shouldReload {
				service.ReloadService()
				if GetServiceByName(service.Name) == nil {
					return
				}
				service = GetServiceByName(service.Name)
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
	if service.state != EnitServiceRunning {
		return nil
	}

	logger.Printf("Stopping service (%s)...", service.Name)
	pid := service.processID

	newServiceStatus := EnitServiceCrashed
	defer func() {
		// Kill remaining child processes
		if pid != 0 {
			syscall.Kill(-pid, syscall.SIGKILL)
		}

		service.state = newServiceStatus
		service.processID = 0

		// Reload service if needed
		if service.shouldReload {
			service.ReloadService()
			if GetServiceByName(service.Name) == nil {
				return
			}
			service = GetServiceByName(service.Name)
		}
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
	} else {
		go func() { service.stopChannel <- true }()

		cmd := exec.Command("/bin/sh", "-c", service.StopCmd)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Check if the process has stopped gracefully, otherwise send sigkill on timeout
	exited := make(chan bool)
	go func() {
		for {
			if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
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

func (service *EnitService) isEnabled() (bool, int) {
	for stage, services := range ReadEnabledServices() {
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

	EnabledServices := ReadEnabledServices()

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

func ReadEnabledServices() (EnabledServices map[int][]string) {
	EnabledServices = make(map[int][]string)

	data, err := os.ReadFile(path.Join(serviceConfigDir, "enabled_services"))
	if err != nil {
		return EnabledServices
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
			return EnabledServices
		}
		err = os.WriteFile(path.Join(serviceConfigDir, "enabled_services"), data, 0644)
		if err != nil {
			return EnabledServices
		}

		return EnabledServices
	}

	return EnabledServices
}
