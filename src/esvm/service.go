package main

import (
	"io"
	"os"
	"os/exec"
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

// Functions will be rewritten at some point to allow enabling unloaded services

func (service *EnitService) isEnabled() bool {
	contents, err := os.ReadFile(path.Join(serviceConfigDir, "enabled_services"))
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if line == service.Name {
			return true
		}
	}

	return false
}

func (service *EnitService) SetEnabled(isEnabled bool) error {
	// Return if service is already in correct state
	if service.isEnabled() == isEnabled {
		return nil
	}

	// Create or open enabled_services file
	file, err := os.OpenFile(path.Join(serviceConfigDir, "enabled_services"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get enabled_services file contents
	contents, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	// Modify contents
	strContents := string(contents)
	if isEnabled {
		strContents += service.Name + "\n"
	} else {
		strContents = strings.ReplaceAll(strContents, service.Name+"\n", "")
	}

	// Write new contents to file
	file.Truncate(0)
	file.Seek(0, 0)
	_, err = file.WriteString(strContents)
	if err != nil {
		return err
	}
	file.Sync()

	return nil
}
