package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path"
)

var commandHandlers = make(map[string]func(conn net.Conn, jsonData map[string]any))

func initSocket() (socket net.Listener, err error) {
	socket, err = net.Listen("unix", path.Join(runtimeServiceDir, "esvm.sock"))
	if err != nil {
		return nil, err
	}

	// Register command handlers
	commandHandlers["reload"] = handleReloadServicesCommand
	commandHandlers["start"] = handleStartServiceCommand
	commandHandlers["stop"] = handleStopServiceCommand
	commandHandlers["restart"] = handleRestartServiceCommand
	commandHandlers["status"] = handleStatusServiceCommand
	commandHandlers["list"] = handleListServicesCommand

	return socket, nil
}

func listenToSocket() {
	conn, err := socket.Accept()
	if err != nil {
		logger.Println("Could not accept socket connection!")
		return
	}

	// Handle the connection in a separate goroutine.
	go func(conn net.Conn) {
		defer conn.Close()

		// Read data from the connection.
		data, err := readAllConn(conn)
		if err != nil {
			logger.Fatalf("Could not read data from socket! Error: %s\n", err)
			return
		}

		// Decoode JSON data
		var jsonData map[string]any
		err = json.Unmarshal(data, &jsonData)
		if err != nil {
			conn.Write(wrapErrorInJson(fmt.Errorf("Invalid JSON")))
			return
		}

		// Get command to execute
		command, ok := jsonData["command"]
		if !ok {
			conn.Write(wrapErrorInJson(fmt.Errorf("'command' field missing")))
			return
		}

		// Get command handler
		commandHandler, ok := commandHandlers[command.(string)]
		if !ok {
			conn.Write(wrapErrorInJson(fmt.Errorf("command (%s) has not been implemented", command.(string))))
			return
		}
		commandHandler(conn, jsonData)
	}(conn)
}

func handleReloadServicesCommand(conn net.Conn, jsonData map[string]any) {
	// Reload services
	Reload()

	conn.Write(wrapSuccessMsgInJson("Services reloaded successfully"))
}

func handleStartServiceCommand(conn net.Conn, jsonData map[string]any) {
	// Get service name from json data
	serviceName, ok := jsonData["service"]
	if !ok {
		conn.Write(wrapErrorInJson(fmt.Errorf("'service' field missing")))
		return
	}

	// Ensure service exists
	service := GetServiceByName(serviceName.(string))
	if service == nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) not found", serviceName.(string))))
		return
	}

	// Start the service
	if err := service.StartService(); err != nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) could not be started", serviceName.(string))))
		return
	}

	conn.Write(wrapSuccessMsgInJson(fmt.Sprintf("Service (%s) has started sucessfully", serviceName.(string))))
}

func handleStopServiceCommand(conn net.Conn, jsonData map[string]any) {
	// Get service name from json data
	serviceName, ok := jsonData["service"]
	if !ok {
		conn.Write(wrapErrorInJson(fmt.Errorf("'service' field missing")))
		return
	}

	// Ensure service exists
	service := GetServiceByName(serviceName.(string))
	if service == nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) not found", serviceName.(string))))
		return
	}

	// Stop the service
	if err := service.StopService(); err != nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) could not be stopped", serviceName.(string))))
		return
	}

	conn.Write(wrapSuccessMsgInJson(fmt.Sprintf("Service (%s) has stopped sucessfully", serviceName.(string))))
}

func handleRestartServiceCommand(conn net.Conn, jsonData map[string]any) {
	// Get service name from json data
	serviceName, ok := jsonData["service"]
	if !ok {
		conn.Write(wrapErrorInJson(fmt.Errorf("'service' field missing")))
		return
	}

	// Ensure service exists
	service := GetServiceByName(serviceName.(string))
	if service == nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) not found", serviceName.(string))))
		return
	}

	// Restart the service
	if err := service.RestartService(); err != nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) could not be restarted", serviceName.(string))))
		return
	}

	conn.Write(wrapSuccessMsgInJson(fmt.Sprintf("Service (%s) has restarted sucessfully", serviceName.(string))))
}

func handleStatusServiceCommand(conn net.Conn, jsonData map[string]any) {
	// Get service name from json data
	serviceName, ok := jsonData["service"]
	if !ok {
		conn.Write(wrapErrorInJson(fmt.Errorf("'service' field missing")))
		return
	}

	// Ensure service exists
	service := GetServiceByName(serviceName.(string))
	if service == nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Service (%s) not found", serviceName.(string))))
		return
	}

	statusMap := make(map[string]any)
	statusMap["name"] = service.Name
	statusMap["description"] = service.Description
	statusMap["state"] = EnitServiceStateNames[service.state]
	statusMap["process_id"] = service.processID

	// Encode map to json string
	newJsonData, err := json.Marshal(statusMap)
	if err != nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Could not encode JSON data")))
		return
	}

	conn.Write(newJsonData)
}

func handleListServicesCommand(conn net.Conn, _ map[string]any) {
	servicesMap := make(map[string]any)
	servicesMap["services"] = make([]map[string]any, 0)

	// Loop through each service
	for _, service := range Services {
		statusMap := make(map[string]any)
		statusMap["name"] = service.Name
		statusMap["description"] = service.Description
		statusMap["state"] = EnitServiceStateNames[service.state]
		statusMap["process_id"] = service.processID
		servicesMap["services"] = append(servicesMap["services"].([]map[string]any), statusMap)
	}

	// Encode map to json string
	newJsonData, err := json.Marshal(servicesMap)
	if err != nil {
		conn.Write(wrapErrorInJson(fmt.Errorf("Could not encode JSON data")))
		return
	}

	conn.Write(newJsonData)
}

func wrapErrorInJson(err error) []byte {
	// Wrap error in struct
	type jsonErrorStruct struct {
		Error string `json:"error"`
	}
	jsonError := jsonErrorStruct{
		Error: err.Error(),
	}

	// Encode struct to json string
	jsonData, _err := json.Marshal(jsonError)
	if _err != nil {
		return nil
	}
	return jsonData
}

func wrapSuccessMsgInJson(msg string) []byte {
	// Wrap message in struct
	type jsonSuccessStruct struct {
		Success string `json:"success"`
	}
	jsonSuccess := jsonSuccessStruct{
		Success: msg,
	}

	// Encode struct to json string
	jsonData, _err := json.Marshal(jsonSuccess)
	if _err != nil {
		return nil
	}
	return jsonData
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
