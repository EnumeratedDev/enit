package main

import (
	"os"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

func isServiceEnabled(service string) (bool, int) {
	for stage, services := range readEnabledServices() {
		if slices.Contains(services, service) {
			return true, stage
		}
	}

	return false, 0
}

func setServiceEnabled(service string, stage int) error {
	// Get current service enabled status
	_, s := isServiceEnabled(service)

	// Return if service is already in correct state
	if s == stage {
		return nil
	}

	EnabledServices := readEnabledServices()

	// Remove service from current stage
	EnabledServices[s] = slices.DeleteFunc(EnabledServices[s], func(name string) bool {
		return name == service
	})
	if len(EnabledServices[s]) == 0 {
		delete(EnabledServices, s)
	}

	// Add service to stage
	if stage != 0 {
		EnabledServices[stage] = append(EnabledServices[stage], service)
	}

	// Save enabled services to file
	data, err := yaml.Marshal(EnabledServices)
	if err != nil {
		return err
	}
	err = os.WriteFile(path.Join(sysconfdir, "esvm/enabled_services"), data, 0644)
	if err != nil {
		return err
	}

	return nil
}

func readEnabledServices() (EnabledServices map[int][]string) {
	EnabledServices = make(map[int][]string)

	data, err := os.ReadFile(path.Join(sysconfdir, "esvm/enabled_services"))
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
		err = os.WriteFile(path.Join(sysconfdir, "esvm/enabled_services"), data, 0644)
		if err != nil {
			return EnabledServices
		}

		return EnabledServices
	}

	return EnabledServices
}
