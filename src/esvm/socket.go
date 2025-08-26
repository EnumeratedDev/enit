package main

import (
	"io"
	"net"
	"path"
	"strings"
)

func initSocket() (socket net.Listener, err error) {
	socket, err = net.Listen("unix", path.Join(runtimeServiceDir, "esvm.sock"))
	if err != nil {
		return nil, err
	}

	return socket, nil
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
